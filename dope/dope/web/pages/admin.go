package pages

import (
	"cmp"
	"context"
	"database/sql"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	ui "dope/dope/web/ui"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"pecheny.me/dopecore/adminusers"
	"pecheny.me/dopecore/session"
	kit "pecheny.me/dopeuikit/kit"
)

const adminUserEnv = "DOPE_ADMIN_USER"

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	return adminusers.RequireAdmin(w, r, adminUserEnv, func() (session.User, bool) {
		return s.h.LookupSession(r)
	})
}

// adminIndexDoc builds the /admin landing page: a link list of admin tools.
func adminIndexDoc() *ui.Doc {
	return &ui.Doc{Nodes: []ui.Node{
		ui.Page(ui.Title("Админка"), ui.PagePublic,
			ui.Publictopbar(Trail([]ui.Item{HomeCrumb()}, "Админка")),
			ui.List(
				ui.Listrow(ui.Href("/admin/create_users"), ui.Listtitle(ui.Text("Создать пользователей"))),
				ui.Listrow(ui.Href("/admin/users"), ui.Listtitle(ui.Text("Пользователи"))),
			),
		),
	}}
}

// adminCreateUsersDoc wraps the kit's create-users body in dope's public
// chrome; pageforms.js drives the copy-textarea select-on-click.
func adminCreateUsersDoc(data adminusers.CreateUsersData) *ui.Doc {
	page := []ui.Item{
		ui.Title("Создать пользователей · Админка"), ui.PagePublic, ui.Classicscripts("dist/pageforms.js"),
		ui.Publictopbar(Trail(AdminCrumbs(), "Создать пользователей")),
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

// sortHeader is a sortable column heading: a small ghost button carrying the
// direction this column would sort in next, and an arrow when it is the active
// one.
func sortHeader(key, label string, s adminusers.Sort) *ui.Element {
	dir, arrow := s.Header(key)
	return ui.Hcell(ui.Button(ui.Ghost, ui.Small(),
		ui.Href("/admin/users?sort="+key+"&dir="+dir), ui.Text(label+arrow),
	))
}

// adminTime renders a stored RFC3339 timestamp for the admin table; a missing
// or unparsable value becomes a dash.
func adminTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// adminUsersDoc builds the /admin/users page: a table of all users, or an empty
// note. System accounts are tagged "(система)".
func adminUsersDoc(data adminUsersData) *ui.Doc {
	var body ui.Item
	if len(data.Users) > 0 {
		rows := []ui.Item{ui.Scroll(), ui.Trow(
			ui.Hcell(ui.Text("ID")), ui.Hcell(ui.Text("Логин")), ui.Hcell(ui.Text("Telegram")),
			sortHeader("last", "Активность", data.Sort), ui.Hcell(ui.Text("Создан")),
		)}
		for _, u := range data.Users {
			nameCell := ui.Cell(ui.Text(u.Username))
			if u.IsSystem {
				nameCell = ui.Cell(ui.Inline(ui.Text(u.Username+" "), ui.Muted(ui.Text("(система)"))))
			}
			rows = append(rows, ui.Trow(
				ui.Cell(ui.Text(strconv.FormatInt(u.ID, 10))),
				nameCell,
				ui.Cell(ui.Text(u.Telegram)),
				ui.Cell(ui.Text(adminTime(u.LastSeenAt))),
				ui.Cell(ui.Text(u.CreatedAt)),
			))
		}
		body = ui.Section(ui.Table(rows...))
	} else {
		body = ui.Empty(ui.Text("Пользователей нет."))
	}
	return &ui.Doc{Nodes: []ui.Node{
		ui.Page(ui.Title("Пользователи · Админка"), ui.PagePublic,
			ui.Publictopbar(Trail(AdminCrumbs(), "Пользователи")),
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
	return store.CollectRows(ctx, s.h.DB(), `
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
		u.CreatedAt = adminTime(u.CreatedAt)
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
		if !s.h.RequireSameOrigin(w, r) {
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
	tx, err := s.h.BeginWriteTx(ctx)
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
