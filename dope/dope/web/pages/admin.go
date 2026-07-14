package pages

import (
	"context"
	"database/sql"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	ui "dope/dope/web/ui"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pecheny.me/dopecore/adminusers"
	"pecheny.me/dopecore/session"
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
			ui.Publictopbar(ui.Title("Админка")),
			ui.List(
				ui.Listrow(ui.Href("/admin/create_users"), ui.Listtitle(ui.Text("Создать пользователей"))),
				ui.Listrow(ui.Href("/admin/users"), ui.Listtitle(ui.Text("Пользователи"))),
			),
		),
	}}
}

// createdSection renders the one-time credentials table + copy-paste textarea
// shown after a create_users submit that created at least one account.
func createdSection(data adminusers.CreateUsersData) *ui.Element {
	tableRows := []ui.Item{ui.Trow(ui.Hcell(ui.Text("Логин")), ui.Hcell(ui.Text("Пароль")))}
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
			ui.Editor(ui.Rows(strconv.Itoa(len(data.Created))), ui.Readonly(), ui.Data("select-all", ""), ui.Text(data.Copyable())),
		),
	)
}

// skippedSection lists usernames that already existed and were left alone.
func skippedSection(skipped []string) *ui.Element {
	return ui.Section(ui.Empty(ui.Text("Уже существуют (пропущены): " + strings.Join(skipped, ", "))))
}

// errorsSection lists usernames rejected as invalid.
func errorsSection(errs []adminusers.UserError) *ui.Element {
	rows := make([]ui.Item, len(errs))
	for i, e := range errs {
		rows[i] = ui.Listrow(ui.Listtitle(ui.Text(e.Username)), ui.Muted(ui.Text(e.Reason)))
	}
	return ui.Section(ui.Empty(ui.Text("Ошибки:")), ui.List(rows...))
}

// createUsersFormSection is the bulk-create form, always shown.
func createUsersFormSection() *ui.Element {
	return ui.Section(
		ui.Form(ui.DirCol, ui.SpaceMD, ui.Method("post"), ui.Action("/admin/create_users"), ui.Autocomplete("off"),
			ui.Field(ui.Label("Логины (по одному в строке)"),
				ui.Editor(ui.Name("usernames"), ui.Rows("10"), ui.Placeholder("anton\nanya_a\ndasha"), ui.Required()),
			),
			ui.Row(ui.Button(ui.Submit(), ui.Text("Создать"))),
		),
	)
}

// adminCreateUsersDoc builds the /admin/create_users page: the bulk-create form,
// plus (after a submit) the outcome — created credentials, skipped usernames, and
// validation errors. pageforms.js drives the copy-textarea select-on-click.
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
			main = append(main, ui.Empty(ui.Text("Не указано ни одного логина.")))
		}
	}
	main = append(main, createUsersFormSection())

	page := []ui.Item{
		ui.Title("Создать пользователей · Админка"), ui.PagePublic, ui.Classicscripts("pageforms.js"),
		ui.Publictopbar(ui.Title("Создать пользователей"), ui.User("/admin"), ui.Userlabel("Админка")),
	}
	page = append(page, main...)
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

type adminUserRow struct {
	ID        int64
	Username  string
	Telegram  string
	IsSystem  bool
	CreatedAt string
}

type adminUsersData struct {
	Users []adminUserRow
}

// adminUsersDoc builds the /admin/users page: a table of all users, or an empty
// note. System accounts are tagged "(система)".
func adminUsersDoc(data adminUsersData) *ui.Doc {
	var body ui.Item
	if len(data.Users) > 0 {
		rows := []ui.Item{ui.Trow(
			ui.Hcell(ui.Text("ID")), ui.Hcell(ui.Text("Логин")),
			ui.Hcell(ui.Text("Telegram")), ui.Hcell(ui.Text("Создан")),
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
				ui.Cell(ui.Text(u.CreatedAt)),
			))
		}
		body = ui.Section(ui.Table(rows...))
	} else {
		body = ui.Empty(ui.Text("Пользователей нет."))
	}
	return &ui.Doc{Nodes: []ui.Node{
		ui.Page(ui.Title("Пользователи · Админка"), ui.PagePublic,
			ui.Publictopbar(ui.Title("Пользователи"), ui.User("/admin"), ui.Userlabel("Админка")),
			body,
		),
	}}
}

// /admin/users — lists all users with their creation timestamp.
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
	RenderDoc(w, s.h.Engine().AssetETags, adminUsersDoc(adminUsersData{Users: users}))
}

func (s *Server) loadAdminUsers(ctx context.Context) ([]adminUserRow, error) {
	return store.CollectRows(ctx, s.h.DB(), `
select id, coalesce(username, ''), coalesce(telegram_username, ''), is_system, created_at
from users
order by created_at desc, id desc`, nil, func(rows *sql.Rows) (adminUserRow, error) {
		var u adminUserRow
		var isSystem int
		if err := rows.Scan(&u.ID, &u.Username, &u.Telegram, &isSystem, &u.CreatedAt); err != nil {
			return u, err
		}
		u.IsSystem = isSystem == 1
		if t, err := time.Parse(time.RFC3339, u.CreatedAt); err == nil {
			u.CreatedAt = t.Format("2006-01-02 15:04")
		}
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
