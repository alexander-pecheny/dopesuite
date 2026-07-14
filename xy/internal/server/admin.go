package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pecheny.me/dopecore/authcred"

	"xy/internal/ui"

	"pecheny.me/dopecore/session"
)

// adminUsername gates the /admin tooling. Defaults to "pecheny"; override with
// XY_ADMIN_USER for other deployments or tests. (Ported from dope.)
func adminUsername() string {
	if v := strings.TrimSpace(os.Getenv("XY_ADMIN_USER")); v != "" {
		return v
	}
	return "pecheny"
}

// requireAdmin resolves the session and confirms it belongs to the configured
// admin. On failure it writes the response itself — a redirect to /login when
// logged out, otherwise a 404 so the page's existence isn't revealed to
// authenticated non-admins — and returns false.
func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request) (session.User, bool) {
	user, ok := s.lookupSession(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return session.User{}, false
	}
	if !user.Username.Valid || user.Username.String != adminUsername() {
		http.NotFound(w, r)
		return session.User{}, false
	}
	return user, true
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

// generatedPasswordAlphabet omits look-alike characters (0/O, 1/l/I) so the
// one-time passwords can be read aloud or retyped without ambiguity.
const generatedPasswordAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
const generatedPasswordLen = 12

func newRandomPassword() (string, error) {
	buf := make([]byte, generatedPasswordLen)
	max := big.NewInt(int64(len(generatedPasswordAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = generatedPasswordAlphabet[n.Int64()]
	}
	return string(buf), nil
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

type adminCreatedUser struct {
	Username string
	Password string
}

type adminUserError struct {
	Username string
	Reason   string
}

type adminCreateUsersData struct {
	Submitted bool
	Created   []adminCreatedUser
	Skipped   []string
	Errors    []adminUserError
}

// Copyable returns the created credentials as tab-separated lines, ready to
// paste into a message to hand out to each new user.
func (d adminCreateUsersData) Copyable() string {
	var b strings.Builder
	for _, u := range d.Created {
		b.WriteString(u.Username)
		b.WriteString("\t")
		b.WriteString(u.Password)
		b.WriteString("\n")
	}
	return b.String()
}

// createdSection renders the one-time credentials table + copy-paste textarea
// shown after a create_users submit that created at least one account.
func createdSection(data adminCreateUsersData) *ui.Element {
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
func errorsSection(errs []adminUserError) *ui.Element {
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
func adminCreateUsersDoc(data adminCreateUsersData) *ui.Doc {
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
// renders them once. (Ported from dope, adapted to xy's users schema.)
func (s *server) HandleAdminCreateUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.renderAdminPage(w, adminCreateUsersDoc(adminCreateUsersData{}))
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

func (s *server) handleAdminCreateUsersSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	usernames := parseUsernameLines(r.PostForm.Get("usernames"))
	data := adminCreateUsersData{Submitted: true}

	now := time.Now()
	err := s.withWriteTx(r.Context(), "admin-create-users", func(ctx context.Context, tx *sql.Tx) error {
		for _, name := range usernames {
			if !validNewUsername(name) {
				data.Errors = append(data.Errors, adminUserError{Username: name, Reason: "недопустимый логин"})
				continue
			}
			var existing int64
			err := tx.QueryRowContext(ctx, `select id from users where username = ?`, name).Scan(&existing)
			if err == nil {
				data.Skipped = append(data.Skipped, name)
				continue
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			password, err := newRandomPassword()
			if err != nil {
				return err
			}
			hash, err := authcred.HashPassword(password)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
insert into users(username, password_hash, created_at, updated_at) values(?, ?, ?, ?)`,
				name, hash, rfc3339(now), rfc3339(now)); err != nil {
				if strings.Contains(err.Error(), "UNIQUE") {
					data.Skipped = append(data.Skipped, name)
					continue
				}
				return err
			}
			data.Created = append(data.Created, adminCreatedUser{Username: name, Password: password})
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderAdminPage(w, adminCreateUsersDoc(data))
}

// parseUsernameLines splits the textarea input into trimmed, de-duplicated
// usernames, preserving first-seen order and dropping blank lines.
func parseUsernameLines(raw string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}
