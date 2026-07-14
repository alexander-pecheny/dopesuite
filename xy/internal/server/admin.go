package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"regexp"
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
		ui.Page(ui.Title("Админка"), ui.PageFull,
			ui.Topbar(ui.Title("Админка")),
			ui.Section(
				ui.List(
					ui.Listrow(ui.Href("/admin/create_users"),
						ui.Listtitle(ui.Text("Создать пользователей")),
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
		ui.Title("Создать пользователей · Админка"), ui.PageFull,
		ui.Topbar(ui.Title("Создать пользователей"),
			ui.Iconlink(ui.Href("/admin"), ui.Label("Админка"), ui.Text("↩")),
		),
	}
	pageItems = append(pageItems, main...)

	return &ui.Doc{Nodes: []ui.Node{ui.Page(pageItems...)}}
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
