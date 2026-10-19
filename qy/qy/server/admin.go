package qyserver

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/adminusers"
	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"code.pecheny.me/pecheny/dopesuite/dopecore/sqlitex"
	kit "pecheny.me/dopeuikit/kit"

	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// Two kinds of admin. The organisers are named by telegram handle in the
// admins table, and QY_ADMINS seeds it at startup so a fresh instance has
// someone to run it. The kit's /admin/create_users page answers to one
// password account instead, QY_ADMIN_USER, which is how staging gets accounts
// without a bot.

const adminUserEnv = "QY_ADMIN_USER"

func isAdminUsername(name string) bool { return name == adminusers.AdminUsername(adminUserEnv) }

func (s *server) isAdmin(ctx context.Context, u session.User) (bool, error) {
	if u.Username.Valid && isAdminUsername(u.Username.String) {
		return true, nil
	}
	if !u.Telegram.Valid || u.Telegram.String == "" {
		return false, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx, `select count(*) from admins where telegram_username = ?`, u.Telegram.String).Scan(&n)
	return n > 0, err
}

// seedAdmins writes the QY_ADMINS handles into the admins table.
func (s *server) seedAdmins(ctx context.Context) error {
	var handles []string
	for _, h := range strings.Split(os.Getenv("QY_ADMINS"), ",") {
		if h = normalHandle(h); h != "" {
			handles = append(handles, h)
		}
	}
	if len(handles) == 0 {
		return nil
	}
	return s.withWriteTx(ctx, "seed-admins", func(ctx context.Context, tx *sql.Tx) error {
		for _, h := range handles {
			if _, err := tx.ExecContext(ctx, `insert or ignore into admins(telegram_username) values(?)`, h); err != nil {
				return err
			}
		}
		return nil
	})
}

// normalHandle is a telegram handle as the users table stores it: no @.
func normalHandle(h string) string { return strings.TrimPrefix(strings.TrimSpace(h), "@") }

func (s *server) requireCreator(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	return adminusers.RequireAdmin(w, r, adminUserEnv, func() (session.User, bool) {
		return s.lookupSession(w, r)
	})
}

// adminStore is qy's half of the create loop: its users table, its write
// transaction. A failing row aborts the batch, so the credentials the page
// shows are exactly the accounts that exist.
type adminStore struct {
	tx  *sql.Tx
	now string
}

func (a adminStore) UserExists(ctx context.Context, username string) (bool, error) {
	var n int
	err := a.tx.QueryRowContext(ctx, `select count(*) from users where username = ?`, username).Scan(&n)
	return n > 0, err
}

func (a adminStore) InsertUser(ctx context.Context, username, passwordHash string) error {
	_, err := a.tx.ExecContext(ctx, `
insert into users(username, password_hash, created_at, updated_at) values(?, ?, ?, ?)`,
		username, passwordHash, a.now, a.now)
	if sqlitex.IsUniqueViolation(err) {
		return adminusers.ErrUserExists
	}
	return err
}

func (s *server) handleAdminCreateUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCreator(w, r); !ok {
		return
	}
	var data adminusers.CreateUsersData
	if r.Method == http.MethodPost {
		if !route.SameOriginUnsafe(r) {
			http.Error(w, str.Server.Error.BadRequest(), http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, str.Server.Error.BadRequest(), http.StatusBadRequest)
			return
		}
		names := adminusers.ParseUsernameLines(r.FormValue("usernames"))
		now := rfc3339(time.Now())
		err := s.withWriteTx(r.Context(), "admin-create-users", func(ctx context.Context, tx *sql.Tx) error {
			var err error
			data, err = adminusers.Creator{
				Store:         adminStore{tx: tx, now: now},
				Validate:      validNewUsername,
				InvalidReason: str.Auth.Username.Format(),
				Policy:        adminusers.AbortOnRowError,
			}.Create(ctx, names)
			return err
		})
		if err != nil {
			s.writePageError(w, r, err)
			return
		}
	}
	page := []ui.Item{
		ui.Title(pageTitle(str.Admin.CreateUsers.Title())), ui.PageSheet,
		ui.Topbar(ui.Nosync(), trailOf([]crumb{{"/admin", str.Admin.Page.Title()}, {"", str.Admin.CreateUsers.Title()}})),
	}
	body, err := ui.Render(&ui.Doc{Nodes: []ui.Node{ui.Page(append(page, kit.AdminCreateUsers(data)...)...)}})
	if err != nil {
		s.writePageError(w, r, err)
		return
	}
	s.writePage(w, r, http.StatusOK, body)
}
