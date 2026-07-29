package server

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"pecheny.me/dopecore/adminusers"
	"pecheny.me/dopecore/session"
	"pecheny.me/dopecore/sqlitex"

	"xy/internal/ui"
)

const adminUserEnv = "XY_ADMIN_USER"

func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	return adminusers.RequireAdmin(w, r, adminUserEnv, func() (session.User, bool) {
		return s.lookupSession(w, r)
	})
}

// sameOrigin guards state-changing admin POSTs: a present Origin header must
// match the request host. (The session cookie is SameSite=Lax, so a cross-site
// POST wouldn't carry it anyway; this is defense in depth.)
func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

// adminUsernameRe allows the same shape the rest of the app uses for logins:
// letters, digits, and ._- (length is checked separately).
var adminUsernameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validNewUsername(name string) bool {
	return len(name) >= 3 && len(name) <= 64 && adminUsernameRe.MatchString(name)
}

// adminIndexDoc builds the /admin landing page: a link list of admin tools.
func adminIndexDoc() *ui.Doc {
	return &ui.Doc{Nodes: []ui.Node{
		ui.Page(ui.Title("Админка"), ui.PageSheet,
			ui.Topbar(ui.Title("Админка")),
			ui.Section(
				ui.List(
					ui.Listrow(ui.Href("/admin/create_users"),
						ui.Listtitle(ui.Text("Создать пользователей")),
					),
					ui.Listrow(ui.Href("/admin/users"),
						ui.Listtitle(ui.Text("Пользователи")),
					),
				),
			),
		),
	}}
}

// createdSection renders the one-time credentials table + copy-paste textarea
// shown after a create_users submit that created at least one account.
func createdSection(data adminusers.CreateUsersData) *ui.Element {
	tableRows := []ui.Item{
		ui.Trow(ui.Hcell(ui.Text("Логин")), ui.Hcell(ui.Text("Пароль"))),
	}
	for _, u := range data.Created {
		tableRows = append(tableRows, ui.Trow(
			ui.Cell(ui.Text(u.Username)),
			ui.Cell(ui.Code(ui.Text(u.Password))),
		))
	}
	return ui.Section(
		ui.Hint(ui.Text("Пароли показаны один раз. Скопируйте и разошлите — пользователи сменят их сами.")),
		ui.Table(tableRows...),
		ui.Field(ui.Label("Для копирования (логин ⇥ пароль)"),
			ui.Editor(ui.Rows(strconv.Itoa(len(data.Created))), ui.Readonly(), ui.Text(data.Copyable())),
		),
	)
}

// skippedSection lists usernames that already existed and were left alone.
func skippedSection(skipped []string) *ui.Element {
	return ui.Section(
		ui.Hint(ui.Text("Уже существуют (пропущены): " + strings.Join(skipped, ", "))),
	)
}

// errorsSection lists usernames rejected as invalid.
func errorsSection(errs []adminusers.UserError) *ui.Element {
	rows := make([]ui.Item, len(errs))
	for i, e := range errs {
		rows[i] = ui.Listrow(
			ui.Listtitle(ui.Text(e.Username)),
			ui.Muted(ui.Text(e.Reason)),
		)
	}
	return ui.Section(
		ui.Hint(ui.Text("Ошибки:")),
		ui.List(rows...),
	)
}

// createUsersFormSection is the bulk-create form, always shown.
func createUsersFormSection() *ui.Element {
	return ui.Section(
		ui.Form(ui.DirCol, ui.SpaceMD, ui.Method("post"), ui.Action("/admin/create_users"), ui.Autocomplete("off"),
			ui.Field(ui.Label("Логины (по одному в строке)"),
				ui.Editor(ui.Name("usernames"), ui.Rows("10"), ui.Placeholder("anton\nanya_a\ndasha"), ui.Required()),
			),
			ui.Row(
				ui.Button(ui.Submit(), ui.Text("Создать")),
			),
		),
	)
}

// adminCreateUsersDoc builds the /admin/create_users page: the bulk-create
// form, plus (after a submit) the outcome — created credentials, skipped
// usernames, and validation errors.
func adminCreateUsersDoc(data adminusers.CreateUsersData) *ui.Doc {
	var main []ui.Item
	if data.Submitted {
		if len(data.Created) > 0 {
			main = append(main, createdSection(data))
		}
		if len(data.Skipped) > 0 {
			main = append(main, skippedSection(data.Skipped))
		}
		if len(data.Errors) > 0 {
			main = append(main, errorsSection(data.Errors))
		}
		if len(data.Created) == 0 && len(data.Skipped) == 0 && len(data.Errors) == 0 {
			main = append(main, ui.Section(ui.Hint(ui.Text("Не указано ни одного логина."))))
		}
	}
	main = append(main, createUsersFormSection())

	pageItems := []ui.Item{
		ui.Title("Создать пользователей · Админка"), ui.PageSheet,
		ui.Topbar(ui.Title("Создать пользователей"),
			ui.Iconlink(ui.Href("/admin"), ui.Label("Админка"), ui.Text("↩️")),
		),
	}
	pageItems = append(pageItems, main...)

	return &ui.Doc{Nodes: []ui.Node{ui.Page(pageItems...)}}
}

type adminUserRow struct {
	ID          int64
	Username    string
	Telegram    string
	Used        int64
	Quota       int64
	Unlimited   bool
	LastLoginAt string // raw RFC3339, empty when the account has no live session
}

// adminTime renders a stored RFC3339 timestamp for the admin tables; a missing
// or unparsable value becomes a dash.
func adminTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// sortAdminUsers reorders in place. Storage can't be an ORDER BY — it is summed
// per user after the query — so both keys are sorted here.
func sortAdminUsers(users []adminUserRow, s adminusers.Sort) {
	if s.Key == "" {
		return
	}
	slices.SortStableFunc(users, func(a, b adminUserRow) int {
		var c int
		if s.Key == "used" {
			c = cmp.Compare(a.Used, b.Used)
		} else {
			c = cmp.Compare(a.LastLoginAt, b.LastLoginAt) // RFC3339 sorts as text; never-logged-in ("") sorts first
		}
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

// storageCell reads "0.16 / 25 МБ" — the unit is stated once, and the admin's
// own uncapped account says so instead of naming a limit.
func storageCell(u adminUserRow) string {
	if u.Unlimited {
		return humanMB(u.Used) + " / ∞"
	}
	return mbNum(u.Used) + " / " + humanMB(u.Quota)
}

// adminUsersDoc builds the /admin/users page: every account with its telegram
// handle, storage against quota, and last login. Login and handle share one
// column — two lines of the same fact, and three columns fit a phone where five
// did not.
func adminUsersDoc(users []adminUserRow, s adminusers.Sort) *ui.Doc {
	var body ui.Item
	if len(users) > 0 {
		rows := []ui.Item{ui.Scroll(), ui.Trow(
			ui.Hcell(ui.Text("Пользователь")),
			sortHeader("used", "Хранилище", s),
			sortHeader("last", "Вход", s),
		)}
		for _, u := range users {
			who := ui.Cell(ui.Text(u.Username))
			if u.Telegram != "" {
				who = ui.Cell(ui.Col(ui.SpaceNone, ui.Line(ui.Text(u.Username)), ui.Muted(ui.Text(u.Telegram))))
			}
			rows = append(rows, ui.Trow(
				who,
				ui.Cell(ui.Text(storageCell(u))),
				ui.Cell(ui.Text(adminTime(u.LastLoginAt))),
			))
		}
		body = ui.Section(ui.Table(rows...))
	} else {
		body = ui.Section(ui.Hint(ui.Text("Пользователей нет.")))
	}
	return &ui.Doc{Nodes: []ui.Node{
		ui.Page(ui.Title("Пользователи · Админка"), ui.PageSheet,
			ui.Topbar(ui.Title("Пользователи"),
				ui.Iconlink(ui.Href("/admin"), ui.Label("Админка"), ui.Text("↩️")),
			),
			body,
		),
	}}
}

// HandleAdminUsers serves /admin/users — the account list, ordered by ?sort/?dir.
func (s *server) HandleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	users, err := s.loadAdminUsers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	order := adminusers.ParseSort(r.URL.Query(), "used", "last")
	sortAdminUsers(users, order)
	s.renderAdminPage(w, adminUsersDoc(users, order))
}

// loadAdminUsers reads every account, then prices each one's storage. The
// per-user sum runs after the row scan, not inside it, so the two queries never
// share a connection.
func (s *server) loadAdminUsers(ctx context.Context) ([]adminUserRow, error) {
	rows, err := s.db.QueryContext(ctx, `
select u.id, u.username, coalesce(u.telegram_username, ''), u.quota_bytes,
       coalesce((select max(s.created_at) from sessions s where s.user_id = u.id), '')
from users u
order by u.created_at desc, u.id desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []adminUserRow
	for rows.Next() {
		var (
			u         adminUserRow
			username  sql.NullString
			lastLogin string
		)
		if err := rows.Scan(&u.ID, &username, &u.Telegram, &u.Quota, &lastLogin); err != nil {
			return nil, err
		}
		u.Username = username.String
		u.Unlimited = quotaExempt(username)
		u.LastLoginAt = lastLogin
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		used, err := storageBytes(ctx, s.db, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Used = used
	}
	return out, nil
}

// HandleAdminLanding serves /admin — a landing page linking to the admin tools.
func (s *server) HandleAdminLanding(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	s.renderAdminPage(w, adminIndexDoc())
}

// HandleAdminCreateUsers serves /admin/create_users: GET shows the form, POST
// bulk-creates username+password accounts with random one-time passwords and
// renders them once.
func (s *server) HandleAdminCreateUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.renderAdminPage(w, adminCreateUsersDoc(adminusers.CreateUsersData{}))
	case http.MethodPost:
		if !sameOrigin(r) {
			http.Error(w, "bad origin", http.StatusForbidden)
			return
		}
		s.handleAdminCreateUsersSubmit(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// renderAdminPage renders an admin page doc with asset-ref versioning + the
// app's strict CSP (the admin pages only load same-origin styles.css/menu.js).
func (s *server) renderAdminPage(w http.ResponseWriter, doc *ui.Doc) {
	rendered, err := ui.Render(doc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body := s.assets.VersionRefs(rendered)
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

// adminUserStore is xy's half of the bulk create: xy's users schema, run inside
// the caller's write transaction.
type adminUserStore struct {
	tx  *sql.Tx
	now time.Time
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
	_, err := st.tx.ExecContext(ctx, `
insert into users(username, password_hash, created_at, updated_at) values(?, ?, ?, ?)`,
		username, passwordHash, rfc3339(st.now), rfc3339(st.now))
	if sqlitex.IsUniqueViolation(err) {
		return adminusers.ErrUserExists
	}
	return err
}

func (s *server) handleAdminCreateUsersSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	usernames := adminusers.ParseUsernameLines(r.PostForm.Get("usernames"))

	now := time.Now()
	var data adminusers.CreateUsersData
	err := s.withWriteTx(r.Context(), "admin-create-users", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		data, err = adminusers.Creator{
			Store:    adminUserStore{tx: tx, now: now},
			Validate: validNewUsername,
			Policy:   adminusers.AbortOnRowError,
		}.Create(ctx, usernames)
		return err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderAdminPage(w, adminCreateUsersDoc(data))
}
