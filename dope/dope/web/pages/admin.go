package pages

import (
	"cmp"
	"context"
	"database/sql"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	"dope/dope/web/route"
	ui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"pecheny.me/dopecore/adminusers"
	"pecheny.me/dopecore/session"
	kit "pecheny.me/dopeuikit/kit"
)

const adminUserEnv = "DOPE_ADMIN_USER"

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	return adminusers.RequireAdmin(w, r, adminUserEnv, func() (session.User, bool) {
		return s.h.Engine().LookupSession(r)
	})
}

// adminIndexDoc builds the /admin landing page: a link list of admin tools.
func adminIndexDoc() *ui.Doc {
	s := dopestrings.Default
	return &ui.Doc{Nodes: []ui.Node{
		ui.Page(ui.Title(s.Admin.Page.Title()), ui.PagePublic,
			ui.Publictopbar(Trail([]ui.Item{HomeCrumb()}, s.Admin.Page.Title())),
			ui.List(
				ui.Listrow(ui.Href("/admin/create_users"), ui.Listtitle(ui.Text(s.Admin.CreateUsers.Name()))),
				ui.Listrow(ui.Href("/admin/users"), ui.Listtitle(ui.Text(s.Admin.Users.Name()))),
			),
		),
	}}
}

// adminCreateUsersDoc wraps the kit's create-users body in dope's public
// chrome; pageforms.js drives the copy-textarea select-on-click.
func adminCreateUsersDoc(data adminusers.CreateUsersData) *ui.Doc {
	s := dopestrings.Default
	page := []ui.Item{
		ui.Title(s.Admin.CreateUsers.Title()), ui.PagePublic, ui.Classicscripts("dist/pageforms.js"),
		ui.Publictopbar(Trail(AdminCrumbs(), s.Admin.CreateUsers.Name())),
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(append(page, kit.AdminCreateUsers(data)...)...)}}
}

type adminUserRow struct {
	ID         int64
	Username   string
	Telegram   string
	IsSystem   bool
	LastSeenAt string // raw RFC3339 of the newest session's last_seen_at; empty when there is none
	CreatedAt  string
}

type adminUsersData struct {
	Users []adminUserRow
	Sort  adminusers.Sort
}

// sortAdminUsers reorders in place by last activity, the one column worth
// ranking on this page. The timestamps are RFC3339, which sorts as text; an
// account that never showed up has none and sorts first ascending.
func sortAdminUsers(users []adminUserRow, s adminusers.Sort) {
	if s.Key != "last" {
		return
	}
	slices.SortStableFunc(users, func(a, b adminUserRow) int {
		c := cmp.Compare(a.LastSeenAt, b.LastSeenAt)
		if s.Desc {
			return -c
		}
		return c
	})
}

// adminUsersDoc builds the /admin/users page: a table of all users, or an empty
// note. System accounts are tagged "(system)".
func adminUsersDoc(data adminUsersData) *ui.Doc {
	s := dopestrings.Default
	var body ui.Item
	if len(data.Users) > 0 {
		rows := []ui.Item{ui.Scroll(), ui.Trow(
			ui.Hcell(ui.Text("ID")), ui.Hcell(ui.Text(s.Admin.Users.ColLogin())), ui.Hcell(ui.Text("Telegram")),
			kit.SortHeader("last", s.Admin.Users.ColActivity(), data.Sort), ui.Hcell(ui.Text(s.Admin.Users.ColCreated())),
		)}
		for _, u := range data.Users {
			nameCell := ui.Cell(ui.Text(u.Username))
			if u.IsSystem {
				nameCell = ui.Cell(ui.Inline(ui.Text(u.Username+" "), ui.Muted(ui.Text(s.Admin.Users.SystemTag()))))
			}
			rows = append(rows, ui.Trow(
				ui.Cell(ui.Text(strconv.FormatInt(u.ID, 10))),
				nameCell,
				ui.Cell(ui.Text(u.Telegram)),
				ui.Cell(ui.Text(kit.AdminTime(u.LastSeenAt))),
				ui.Cell(ui.Text(u.CreatedAt)),
			))
		}
		body = ui.Section(ui.Table(rows...))
	} else {
		body = ui.Empty(ui.Text(s.Admin.Users.Empty()))
	}
	return &ui.Doc{Nodes: []ui.Node{
		ui.Page(ui.Title(s.Admin.Users.Title()), ui.PagePublic,
			ui.Publictopbar(Trail(AdminCrumbs(), s.Admin.Users.Name())),
			body,
		),
	}}
}

// /admin/users — every account with its handle, last activity and creation time.
// Activity is the newest session's last_seen_at, not its created_at: sessions
// slide, so someone who visits daily on the same cookie never logs in again.
func (s *Server) HandleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/users" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	users, err := s.loadAdminUsers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	order := adminusers.ParseSort(r.URL.Query(), "last")
	sortAdminUsers(users, order)
	RenderDoc(w, s.h.Engine().AssetETags, adminUsersDoc(adminUsersData{Users: users, Sort: order}))
}

func (s *Server) loadAdminUsers(ctx context.Context) ([]adminUserRow, error) {
	return store.CollectRows(ctx, s.h.Engine().DB, `
select u.id, coalesce(u.username, ''), coalesce(u.telegram_username, ''), u.is_system, u.created_at,
       coalesce((select max(s.last_seen_at) from sessions s where s.user_id = u.id), '')
from users u
order by u.created_at desc, u.id desc`, nil, func(rows *sql.Rows) (adminUserRow, error) {
		var u adminUserRow
		var isSystem int
		if err := rows.Scan(&u.ID, &u.Username, &u.Telegram, &isSystem, &u.CreatedAt, &u.LastSeenAt); err != nil {
			return u, err
		}
		u.IsSystem = isSystem == 1
		u.CreatedAt = kit.AdminTime(u.CreatedAt)
		return u, nil
	})
}

// /admin — landing page with links to admin tools.
func (s *Server) HandleAdminLanding(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	RenderDoc(w, s.h.Engine().AssetETags, adminIndexDoc())
}

// /admin/create_users — GET shows the form; POST bulk-creates users with random
// one-time passwords and renders them once.
func (s *Server) HandleAdminCreateUsers(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/create_users" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if _, ok := s.requireAdmin(w, r); !ok {
			return
		}
		s.renderAdminCreateUsers(w, adminusers.CreateUsersData{})
	case http.MethodPost:
		if _, ok := s.requireAdmin(w, r); !ok {
			return
		}
		if !route.SameOriginUnsafe(w, r) {
			return
		}
		s.handleAdminCreateUsersSubmit(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) renderAdminCreateUsers(w http.ResponseWriter, data adminusers.CreateUsersData) {
	RenderDoc(w, s.h.Engine().AssetETags, adminCreateUsersDoc(data))
}

// adminUserStore is dope's half of the bulk create: dope's users schema, run
// inside the caller's write transaction.
type adminUserStore struct {
	tx *sql.Tx
}

func (st adminUserStore) UserExists(ctx context.Context, username string) (bool, error) {
	var id int64
	err := st.tx.QueryRowContext(ctx, `select id from users where username = ?`, username).Scan(&id)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

func (st adminUserStore) InsertUser(ctx context.Context, username, passwordHash string) error {
	now := util.UtcNow()
	_, err := st.tx.ExecContext(ctx, `
insert into users(telegram_user_id, telegram_username, username, password_hash, password_salt, is_system, created_at, updated_at)
values(null, null, ?, ?, null, 0, ?, ?)`, username, passwordHash, now, now)
	if util.IsUniqueViolation(err) {
		return adminusers.ErrUserExists
	}
	return err
}

func (s *Server) handleAdminCreateUsersSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	usernames := adminusers.ParseUsernameLines(r.PostForm.Get("usernames"))

	ctx := r.Context()
	tx, err := s.h.Engine().BeginWriteTx(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	data, _ := adminusers.Creator{
		Store:    adminUserStore{tx: tx},
		Validate: util.ValidUsername,
		Policy:   adminusers.CollectRowErrors,
	}.Create(ctx, usernames)

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderAdminCreateUsers(w, data)
}
