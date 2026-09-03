package hostpages

import (
	"database/sql"
	"dope/dope/web/pages"
	"dope/dope/web/route"
	ui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	corei18n "pecheny.me/dopecore/i18nstrings"
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
	s := dopestrings.Default
	return []ui.Item{
		ui.Data("jump-label", s.Host.Pages.JumpLabel()),
		ui.Data("jump-href", "/"),
		ui.Data("jump-title", s.Host.Pages.JumpTitle()),
	}
}

// hostLoggedInDoc builds the /host landing for a signed-in organizer: their fests
// grouped into current/future/past disclosures, and the create-fest form.
func hostLoggedInDoc(data hostLandingData) *ui.Doc {
	s := dopestrings.Default
	page := []ui.Item{ui.Title(s.Host.Pages.LandingTitle(data.Username)), ui.PagePublic}
	page = append(page, jumpViewerNav()...)
	page = append(page, ui.Publictopbar(pages.Trail([]ui.Item{pages.HomeCrumb()}, s.Host.Pages.LandingCrumb())))

	if data.Error != "" {
		page = append(page, ui.Empty(ui.Text(data.Error)))
	}
	if len(data.Groups) > 0 {
		for _, g := range data.Groups {
			fests := make([]ui.Item, 0, len(g.Fests))
			for _, f := range g.Fests {
				title := f.Title
				if !f.IsPublic {
					title = s.Host.Pages.FestRowUnlisted(f.Title)
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
		page = append(page, ui.Empty(ui.Text(s.Host.Pages.FestsEmpty())))
	}

	page = append(page, ui.Section(ui.Details(
		ui.Summary(ui.Btn(), ui.Text(s.Host.Pages.CreateFestSummary())),
		ui.Form(ui.DirCol, ui.Method("post"), ui.Action("/host/fest"), ui.Autocomplete("off"),
			ui.Field(ui.Label(s.Host.Pages.TitleLabel()), ui.Textfield(ui.Name("title"), ui.Required())),
			ui.Field(ui.Label(s.Host.Pages.DescriptionLabel()), ui.Editor(ui.Name("description"), ui.Rows("4"))),
			ui.Field(ui.Label(s.Host.Pages.StartDateLabel()), ui.Textfield(ui.Name("start_date"), ui.Placeholder("2026-05-15"))),
			ui.Field(ui.Label(s.Host.Pages.EndDateLabel()), ui.Textfield(ui.Name("end_date"), ui.Placeholder("2026-05-17"))),
			ui.Field(ui.Label(s.Host.Pages.RatingIdLabel()), ui.Textfield(ui.Name("rating_id"), ui.Inputmode("numeric"))),
			ui.Checkbox(ui.Name("is_public"), ui.Value("1"), ui.Text(s.Host.Pages.PublicLabel())),
			ui.Row(ui.Button(ui.Submit(), ui.Text(s.Host.Pages.CreateSubmit()))),
		),
	)))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

type profileData struct {
	HasPassword bool
	Username    string
	Telegram    string
}

// identitySection renders who you are logged in as. Either identity can be
// missing: a Telegram-only account has no username until it picks one, and a
// username/password account never links a Telegram handle.
func identitySection(data profileData) []ui.Item {
	s := dopestrings.Default
	var lines []ui.Item
	if data.Username != "" {
		lines = append(lines, ui.Hint(ui.Inline(ui.Text(s.Host.Pages.IdentityUsernameLead()), ui.Strong(ui.Text(data.Username)), ui.Text("."))))
	}
	if data.Telegram != "" {
		lines = append(lines, ui.Hint(ui.Inline(ui.Text("Telegram: "), ui.Strong(ui.Text("@"+data.Telegram)), ui.Text("."))))
	}
	return lines
}

// profileDoc builds the /profile page: who you are, the set/change-password form
// (driven by profile.js via #passwordForm + data-has-password) and a logout form.
func profileDoc(data profileData) *ui.Doc {
	s := dopestrings.Default
	action := s.Host.Pages.PasswordSetSubmit()
	hasPassword := "0"
	if data.HasPassword {
		action = s.Host.Pages.PasswordChangeSubmit()
		hasPassword = "1"
	}
	form := []ui.Item{ui.ID("passwordForm"), ui.DirCol, ui.Autocomplete("off"), ui.Data("has-password", hasPassword)}
	if data.HasPassword {
		form = append(form, ui.Password(ui.ID("currentPassword"), ui.Name("current_password"),
			ui.Placeholder(s.Host.Pages.PasswordCurrentPlaceholder()), ui.Autocomplete("current-password"), ui.Required()))
	}
	form = append(form,
		ui.Password(ui.ID("newPassword"), ui.Name("new_password"),
			ui.Placeholder(s.Host.Pages.PasswordNewPlaceholder()), ui.Autocomplete("new-password"), ui.Minlength("8"), ui.Required()),
		ui.Password(ui.ID("confirmPassword"), ui.Name("confirm_password"),
			ui.Placeholder(s.Host.Pages.PasswordConfirmPlaceholder()), ui.Autocomplete("new-password"), ui.Required()),
		ui.Button(ui.Submit(), ui.Text(action)),
	)
	page := []ui.Item{ui.Title(s.Host.Pages.ProfileTitle()), ui.PagePublic, ui.Classicscripts("dist/profile.js"),
		ui.Publictopbar(pages.Trail(pages.HostCrumbs(), s.Host.Pages.ProfileCrumb())),
	}
	if lines := identitySection(data); len(lines) > 0 {
		page = append(page, ui.Section(lines...))
	}
	page = append(page,
		ui.Section(
			ui.Hint(ui.Text(action)),
			ui.Form(form...),
			ui.Message(ui.ID("passwordMessage")),
		),
		ui.Form(ui.Method("post"), ui.Action("/profile/logout"),
			ui.Button(ui.Submit(), ui.Text(s.Host.Pages.LogoutSubmit())),
		),
	)
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
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
		username = dopestrings.Default.Host.Pages.UsernameFallback()
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
		var hash, username, telegram sql.NullString
		if err := s.h.Engine().DB.QueryRowContext(r.Context(),
			`select password_hash, username, telegram_username from users where id = ?`,
			user.UserID).Scan(&hash, &username, &telegram); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pages.RenderDoc(w, s.h.Engine().AssetETags, profileDoc(profileData{
			HasPassword: hash.Valid && hash.String != "",
			Username:    username.String,
			Telegram:    strings.TrimPrefix(telegram.String, "@"),
		}))
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
	if !route.SameOriginUnsafe(w, r) {
		return
	}
	s.h.LogoutSession(r)
	session.ClearCookie(w)
	http.Redirect(w, r, "/host", http.StatusSeeOther)
}

func parsePositiveFormInt(form url.Values, key, label string, min, max int) (int, error) {
	raw := strings.TrimSpace(form.Get(key))
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return 0, corei18n.User(dopestrings.Default.Host.Pages.ErrorIntRange(label, strconv.Itoa(min), strconv.Itoa(max)))
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
		return 0, corei18n.User(dopestrings.Default.Host.Pages.ErrorIntRange(label, strconv.Itoa(min), strconv.Itoa(max)))
	}
	return value, nil
}
