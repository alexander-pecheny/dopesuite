package spliffserver

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"pecheny.me/dopecore/adminusers"
	"pecheny.me/dopecore/session"
	"pecheny.me/dopecore/sqlitex"
	kitstrings "pecheny.me/dopeuikit/i18nstrings"
	kit "pecheny.me/dopeuikit/kit"

	"spliff/spliff/web/route"
	"spliff/spliff/web/ui"

	spliffstrings "spliff/i18nstrings"
)

// The /admin bulk-create page, the same one xy and dope serve over
// dopecore/adminusers (root docs/adr/0004). It is how a password account comes
// to exist on an instance with no telegram bot — staging, a dev checkout, the
// verify run — and how a new circle of friends is seeded without asking each of
// them to talk to a bot first.

const adminUserEnv = "SPLIFF_ADMIN_USER"

func isAdminUsername(name string) bool { return name == adminusers.AdminUsername(adminUserEnv) }

func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	return adminusers.RequireAdmin(w, r, adminUserEnv, func() (session.User, bool) {
		return s.lookupSession(w, r)
	})
}

// adminStore is Spliff's half of the create loop: its users table, its write
// transaction. The whole batch runs in one transaction and a failing row aborts
// it, so the credentials the page shows are exactly the accounts that exist.
type adminStore struct {
	ctx context.Context
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

func (s *server) handleAdminLanding(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	str := spliffstrings.Default
	doc := &kit.Doc{Nodes: []kit.Node{
		kit.Page(kit.Title(str.Admin.Page.Title()), kit.PageSheet,
			kit.Topbar(kit.Title(str.Admin.Page.Title())),
			kit.Section(kit.List(
				kit.Listrow(kit.Href("/admin/create_users"),
					kit.Listtitle(kit.Text(str.Admin.CreateUsers.Name()))),
			)),
		),
	}}
	s.renderAdmin(w, r, doc)
}

func (s *server) handleAdminCreateUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	str := spliffstrings.Default
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
				Store:         adminStore{ctx: ctx, tx: tx, now: now},
				Validate:      validNewUsername,
				InvalidReason: str.Auth.Username.Format(),
				Policy:        adminusers.AbortOnRowError,
			}.Create(ctx, names)
			return err
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	items := []kit.Item{
		kit.Title(str.Admin.CreateUsers.Title()), kit.PageSheet,
		kit.Topbar(kit.Title(str.Admin.CreateUsers.Name()),
			kit.Iconlink(kit.Href("/admin"), kit.Label(str.Admin.Page.Title()), kit.IconArrowLeft),
		),
	}
	doc := &kit.Doc{Nodes: []kit.Node{kit.Page(append(items, kit.AdminCreateUsersIn(kitstrings.EN, data)...)...)}}
	s.renderAdmin(w, r, doc)
}

func (s *server) renderAdmin(w http.ResponseWriter, r *http.Request, doc *kit.Doc) {
	body, err := ui.Render(doc)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.writePage(w, r, body)
}
