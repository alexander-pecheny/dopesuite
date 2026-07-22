package hostpages

import (
	"database/sql"
	"dope/dope/domain/core"
	"dope/dope/platform/roles"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	ui "dope/dope/web/ui"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pecheny.me/dopecore/session"
)

type hostLandingData struct {
	LoggedIn bool
	Username string
	Groups   []hostFestGroup
	Error    string
}

// jumpViewerNav are the body data-jump-* attrs menu.js reads to offer a jump to
// the public viewer page from the host landing.
func jumpViewerNav() []ui.Item {
	return []ui.Item{
		ui.Data("jump-label", "Страница зрителя"),
		ui.Data("jump-href", "/"),
		ui.Data("jump-title", "Открыть зрительскую страницу"),
	}
}

// hostLoggedInDoc builds the /host landing for a signed-in organizer: their fests
// grouped into current/future/past disclosures, and the create-fest form.
func hostLoggedInDoc(data hostLandingData) *ui.Doc {
	page := []ui.Item{ui.Title("Мои фесты · " + data.Username), ui.PagePublic}
	page = append(page, jumpViewerNav()...)
	page = append(page, ui.Publictopbar(ui.Title("Мои фесты")))

	if data.Error != "" {
		page = append(page, ui.Empty(ui.Text(data.Error)))
	}
	if len(data.Groups) > 0 {
		for _, g := range data.Groups {
			fests := make([]ui.Item, 0, len(g.Fests))
			for _, f := range g.Fests {
				title := f.Title
				if !f.IsPublic {
					title += " · непубличный"
				}
				row := []ui.Item{ui.Href("/host/fest/" + f.Ref()), ui.Listtitle(ui.Text(title))}
				if f.Dates != "" {
					row = append(row, ui.Muted(ui.Text(f.Dates)))
				}
				fests = append(fests, ui.Listrow(row...))
			}
			page = append(page, ui.Festgroup(ui.Open(), ui.Title(g.Title), ui.List(fests...)))
		}
	} else {
		page = append(page, ui.Empty(ui.Text("Фестов пока нет.")))
	}

	page = append(page, ui.Section(ui.Details(
		ui.Summary(ui.Btn(), ui.Text("Создать фест")),
		ui.Form(ui.DirCol, ui.Method("post"), ui.Action("/host/fest"), ui.Autocomplete("off"),
			ui.Field(ui.Label("Название"), ui.Textfield(ui.Name("title"), ui.Required())),
			ui.Field(ui.Label("Описание (markdown)"), ui.Editor(ui.Name("description"), ui.Rows("4"))),
			ui.Field(ui.Label("Дата начала (YYYY-MM-DD)"), ui.Textfield(ui.Name("start_date"), ui.Placeholder("2026-05-15"))),
			ui.Field(ui.Label("Дата окончания"), ui.Textfield(ui.Name("end_date"), ui.Placeholder("2026-05-17"))),
			ui.Field(ui.Label("rating.chgk.info ID (опционально)"), ui.Textfield(ui.Name("rating_id"), ui.Inputmode("numeric"))),
			ui.Checkbox(ui.Name("is_public"), ui.Value("1"), ui.Text("Публичный")),
			ui.Row(ui.Button(ui.Submit(), ui.Text("Создать"))),
		),
	)))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

type profileData struct {
	HasPassword bool
}

// profileDoc builds the /profile page: the set/change-password form (driven by
// profile.js via #passwordForm + data-has-password) and a logout form.
func profileDoc(data profileData) *ui.Doc {
	action := "Установить пароль"
	hasPassword := "0"
	if data.HasPassword {
		action = "Сменить пароль"
		hasPassword = "1"
	}
	form := []ui.Item{ui.ID("passwordForm"), ui.DirCol, ui.Autocomplete("off"), ui.Data("has-password", hasPassword)}
	if data.HasPassword {
		form = append(form, ui.Password(ui.ID("currentPassword"), ui.Name("current_password"),
			ui.Placeholder("Текущий пароль"), ui.Autocomplete("current-password"), ui.Required()))
	}
	form = append(form,
		ui.Password(ui.ID("newPassword"), ui.Name("new_password"),
			ui.Placeholder("Новый пароль"), ui.Autocomplete("new-password"), ui.Minlength("8"), ui.Required()),
		ui.Password(ui.ID("confirmPassword"), ui.Name("confirm_password"),
			ui.Placeholder("Повторите новый пароль"), ui.Autocomplete("new-password"), ui.Required()),
		ui.Button(ui.Submit(), ui.Text(action)),
	)
	return &ui.Doc{Nodes: []ui.Node{
		ui.Page(ui.Title("Профиль"), ui.PagePublic, ui.Classicscripts("profile.js"),
			ui.Publictopbar(ui.Title("Профиль")),
			ui.List(ui.Listrow(ui.Href("/host"), ui.Listtitle(ui.Text("← Назад к списку турниров")))),
			ui.Section(
				ui.Hint(ui.Text(action)),
				ui.Form(form...),
				ui.Message(ui.ID("passwordMessage")),
			),
			ui.Form(ui.Method("post"), ui.Action("/profile/logout"),
				ui.Button(ui.Submit(), ui.Text("Разлогиниться")),
			),
		),
	}}
}

// /host — landing page.
func (s *Server) HandleHostLanding(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/host" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.renderHostLanding(w, r, "")
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) renderHostLanding(w http.ResponseWriter, r *http.Request, errMsg string) {
	user, ok := s.h.Engine().LookupSession(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	fests, err := s.loadHostFests(r.Context(), user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	username := ""
	if user.Username.Valid {
		username = user.Username.String
	}
	if username == "" {
		username = "Профиль"
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostLoggedInDoc(hostLandingData{
		LoggedIn: true,
		Username: username,
		Groups:   groupHostFests(fests, time.Now().Format("2006-01-02")),
		Error:    errMsg,
	}))
}

func (s *Server) HandleProfilePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/profile" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		user, ok := s.h.Engine().LookupSession(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		var hash sql.NullString
		if err := s.h.Engine().DB.QueryRowContext(r.Context(),
			`select password_hash from users where id = ?`, user.UserID).Scan(&hash); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pages.RenderDoc(w, s.h.Engine().AssetETags, profileDoc(profileData{HasPassword: hash.Valid && hash.String != ""}))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) HandleProfileLogout(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/profile/logout" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.h.RequireSameOrigin(w, r) {
		return
	}
	s.h.LogoutSession(r)
	session.ClearCookie(w)
	http.Redirect(w, r, "/host", http.StatusSeeOther)
}

// /host/<...> — auth-gated subpaths.
//   - /host/fest              POST: create fest
//   - /host/fest/{id}         GET: dashboard, POST: update
//   - /host/fest/{id}/game/{gid}/...   serves host.html for the EK match grid
func (s *Server) HandleHostRouter(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/host/")
	if rest == "" || rest == "/" {
		http.Redirect(w, r, "/host", http.StatusSeeOther)
		return
	}
	// SameSite=Lax on the session cookie is the primary CSRF defense, but
	// also reject cross-origin form submits explicitly so a single browser
	// quirk cannot escalate into delete-fest / assign-host from another tab.
	if !s.h.RequireSameOrigin(w, r) {
		return
	}
	user, ok := s.h.Engine().LookupSession(r)
	if !ok {
		http.Redirect(w, r, "/host", http.StatusSeeOther)
		return
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if parts[0] != "fest" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 {
		// /host/fest — only POST (create)
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/host", http.StatusSeeOther)
			return
		}
		s.handleHostCreateFest(w, r, user)
		return
	}
	id, err := store.ResolveFestID(r.Context(), s.h.Engine().DB, parts[1])
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if id <= 0 {
		http.NotFound(w, r)
		return
	}
	role, err := festaccess.FestUserRoleFromQuery(r.Context(), s.h.Engine().DB, id, user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if role == "" {
		http.Redirect(w, r, "/host", http.StatusSeeOther)
		return
	}
	requireManageFest := func() bool {
		if roles.CanManageFest(role) {
			return true
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	requireCreator := func() bool {
		if roles.CanDeleteFest(role) {
			return true
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostFestDashboard(w, r, id, hostDashMessages{})
		case http.MethodPost:
			if !requireManageFest() {
				return
			}
			s.handleHostUpdateFest(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 3 && parts[2] == "teams" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.renderHostFestTeams(w, r, id)
		return
	}
	if len(parts) == 3 && parts[2] == "players" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.renderHostFestPlayers(w, r, id)
		return
	}
	if len(parts) == 4 && parts[2] == "players" && parts[3] == "overrides" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleHostAddPlayerOverride(w, r, id)
		return
	}
	if len(parts) == 3 && parts[2] == "import" {
		if !requireManageFest() {
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostSchemeImportPage(w, r, id, "", "")
		case http.MethodPost:
			s.handleHostImportScheme(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 3 && parts[2] == "access" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleHostSaveAccess(w, r, id, user.UserID)
		return
	}
	if len(parts) == 3 && parts[2] == "delete" {
		if !requireCreator() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleHostDeleteFest(w, r, id, user.UserID)
		return
	}
	if len(parts) == 4 && parts[2] == "game" && parts[3] == "new" {
		if !requireManageFest() {
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostCreateGamePage(w, r, id, "", "")
		case http.MethodPost:
			s.handleHostCreateGame(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 5 && parts[2] == "game" && (parts[4] == "delete" || parts[4] == "clear") {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		gameID, err := s.h.ResolveGameID(r.Context(), id, parts[3])
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if gameID <= 0 {
			http.NotFound(w, r)
			return
		}
		if parts[4] == "clear" {
			s.handleHostClearGame(w, r, id, gameID)
		} else {
			s.handleHostDeleteGame(w, r, id, gameID)
		}
		return
	}
	if len(parts) == 5 && parts[2] == "game" && parts[4] == "settings" {
		if !requireManageFest() {
			return
		}
		gameID, err := s.h.ResolveGameID(r.Context(), id, parts[3])
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if gameID <= 0 {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostGameSettings(w, r, id, gameID, "")
		case http.MethodPost:
			s.handleHostUpdateGameSettings(w, r, id, gameID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 3 && parts[2] == "numbers" {
		if !requireManageFest() {
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.pages().RenderHostFestNumbers(w, r, id, "", "", nil)
		case http.MethodPost:
			s.pages().HandleHostSaveFestNumbers(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 4 && parts[2] == "numbers" && parts[3] == "auto" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.pages().HandleHostAutoFestNumbers(w, r, id)
		return
	}
	if len(parts) == 4 && parts[2] == "numbers" && parts[3] == "clear" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.pages().HandleHostClearFestNumbers(w, r, id)
		return
	}
	if len(parts) == 5 && parts[2] == "numbers" && parts[3] == "import" {
		if !requireManageFest() {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch parts[4] {
		case "match":
			s.pages().HandleHostFestNumbersImportMatch(w, r, id)
		case "apply":
			s.pages().HandleHostFestNumbersImportApply(w, r, id)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if len(parts) == 4 && parts[2] == "rating" && parts[3] == "import" {
		if !requireManageFest() {
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.renderHostRatingImportPage(w, r, id, "", "")
		case http.MethodPost:
			s.handleHostImportRatingRoster(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	// /host/fest/{id}/audit              → per-game history index
	// /host/fest/{id}/audit/{gid}        → one game's edit history
	// /host/fest/{id}/audit/{gid}/revert → per-game derived revert (POST)
	if parts[2] == "audit" {
		if !requireManageFest() {
			return
		}
		switch {
		case len(parts) == 3:
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.pages().RenderHostFestAudit(w, r, id, "", "")
			return
		case len(parts) == 4 || (len(parts) == 5 && parts[4] == "revert"):
			gid, err := strconv.ParseInt(parts[3], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if len(parts) == 5 { // revert
				if r.Method != http.MethodPost {
					http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
					return
				}
				s.pages().HandleGameRevert(w, r, id, gid)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.pages().RenderGameJournal(w, r, id, gid, "", "")
			return
		default:
			http.NotFound(w, r)
			return
		}
	}
	// /host/fest/{id}/game/{gid}[/...] → serve host.html / od.html / si.html.
	if !isHostGameSubPath(parts[2:]) {
		http.NotFound(w, r)
		return
	}
	s.serveHostGamePage(w, r, id, parts[2:])
}

func (s *Server) serveHostGamePage(w http.ResponseWriter, r *http.Request, festID int64, parts []string) {
	gameID, err := s.h.ResolveGameID(r.Context(), festID, parts[1])
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if gameID <= 0 {
		http.NotFound(w, r)
		return
	}
	var gameType string
	if err := s.h.Engine().DB.QueryRowContext(r.Context(), `select game_type from games where id = ? and fest_id = ?`, gameID, festID).Scan(&gameType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	scope := core.FestScope{FestID: festID, GameID: gameID}
	switch gameType {
	case "od":
		s.h.ServeGameHTMLWithInit(w, r, "static/od.html", scope)
	case "si", "ksi":
		s.h.ServeGameHTMLWithInit(w, r, "static/si.html", scope)
	default:
		s.h.ServeHostHTMLWithInit(w, r, scope, parts)
	}
}

func isHostGameSubPath(parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	if parts[0] != "game" || parts[1] == "" {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	switch parts[2] {
	case "venues", "seed-import", "stats", "roster":
		return len(parts) == 3
	case "matches", "stage":
		return len(parts) == 4 && parts[3] != ""
	}
	return false
}

func parsePositiveFormInt(form url.Values, key, label string, min, max int) (int, error) {
	raw := strings.TrimSpace(form.Get(key))
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return 0, fmt.Errorf("%s должно быть от %d до %d", label, min, max)
	}
	return value, nil
}

// parseNonNegativeFormInt is like parsePositiveFormInt but treats an empty field
// as min (used for the sticker max-count inputs, where a blank or 0 means "the
// team has none of this sticker").
func parseNonNegativeFormInt(form url.Values, key, label string, min, max int) (int, error) {
	raw := strings.TrimSpace(form.Get(key))
	if raw == "" {
		return min, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return 0, fmt.Errorf("%s должно быть от %d до %d", label, min, max)
	}
	return value, nil
}
