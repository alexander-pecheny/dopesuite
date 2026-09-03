package hostpages

import (
	"database/sql"
	"dope/dope/domain/venues"
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
	Venues   []venues.Venue
	Error    string
	// Form is what was just submitted and refused, so the form comes back
	// filled instead of blank; Open names the disclosure to leave open.
	Form url.Values
	Open string
}

// kept reads a refused form's value, so nothing a host typed is lost.
func (d hostLandingData) kept(field string) string { return d.Form.Get(field) }

func (d hostLandingData) checked(field string) bool { return d.Form.Get(field) == "1" }

// jumpViewerNav are the body data-jump-* attrs menu.js reads to offer a jump to
// the public viewer page from the host landing.
func jumpViewerNav() []ui.Item {
	s := dopestrings.Default
	return []ui.Item{
		ui.Data("jump-label", s.Host.Pages.JumpLabel()),
		ui.Data("jump-href", "/"),
		ui.Data("jump-title", s.Host.Pages.JumpTitle()),
		ui.Data("jump-icon", "eye"),
	}
}

// hostLoggedInDoc builds the /host landing for a signed-in organizer: their
// Фесты, their Площадки, and the form that makes another of each.
func hostLoggedInDoc(data hostLandingData) *ui.Doc {
	s := dopestrings.Default
	page := []ui.Item{ui.Title(s.Host.Pages.LandingTitle(data.Username)), ui.PagePublic,
		ui.Classicscripts("dist/pageforms.js dist/roster-editor.js")}
	page = append(page, jumpViewerNav()...)
	page = append(page, ui.Publictopbar(pages.Trail([]ui.Item{pages.HomeCrumb()}, s.Host.Pages.LandingCrumb())))

	if data.Error != "" && data.Open == "" {
		page = append(page, ui.Empty(ui.Text(data.Error)))
	}
	page = append(page, hostLandingFests(data), hostLandingVenues(data))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

// hostLandingFests is the Фесты the user runs, and the form that makes one.
// It and Площадки are the same shape: a heading, what there is, and the way to
// make another.
func hostLandingFests(data hostLandingData) *ui.Element {
	s := dopestrings.Default
	sect := []ui.Item{ui.Subhead(ui.Text("Фесты"))}
	if len(data.Groups) == 0 {
		sect = append(sect, ui.Empty(ui.Text(s.Host.Pages.FestsEmpty())))
	}
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
		sect = append(sect, ui.Festgroup(ui.Open(), ui.Title(g.Title), ui.List(fests...)))
	}
	return ui.Section(append(sect, festCreateForm(data))...)
}

func festCreateForm(data hostLandingData) *ui.Element {
	s := dopestrings.Default
	items := []ui.Item{ui.Summary(ui.Btn(), ui.Text(s.Host.Pages.CreateFestSummary()))}
	if data.Open == "fest" {
		items = append([]ui.Item{ui.Open()}, items...)
		if data.Error != "" {
			items = append(items, ui.Hint(ui.HintDanger, ui.Text(data.Error)))
		}
	}
	return ui.Details(append(items,
		ui.Form(ui.DirCol, ui.Method("post"), ui.Action("/host/fest"), ui.Autocomplete("off"),
			ui.Field(ui.Label(s.Host.Pages.TitleLabel()), ui.Textfield(ui.Name("title"), ui.Value(data.kept("title")), ui.Required())),
			ui.Field(ui.Label(s.Host.Pages.DescriptionLabel()), ui.Editor(ui.Name("description"), ui.Rows("4"), ui.Text(data.kept("description")))),
			ui.Field(ui.Label(s.Host.Pages.StartDateLabel()), ui.Textfield(ui.Name("start_date"), ui.Value(data.kept("start_date")), ui.Placeholder("2026-05-15"))),
			ui.Field(ui.Label(s.Host.Pages.EndDateLabel()), ui.Textfield(ui.Name("end_date"), ui.Value(data.kept("end_date")), ui.Placeholder("2026-05-17"))),
			ui.Field(ui.Label(s.Host.Pages.RatingIdLabel()), ui.Textfield(ui.Name("rating_id"), ui.Value(data.kept("rating_id")), ui.Inputmode("numeric"))),
			checkboxKept("is_public", s.Host.Pages.PublicLabel(), data.checked("is_public")),
			ui.Row(ui.Button(ui.Submit(), ui.Text(s.Host.Pages.CreateSubmit()))),
		))...)
}

// checkboxKept is a tickbox that comes back ticked when the refused form had it.
func checkboxKept(name, label string, on bool) *ui.Element {
	items := []ui.Item{ui.Name(name), ui.Value("1"), ui.Text(label)}
	if on {
		items = append(items, ui.Checked())
	}
	return ui.Checkbox(items...)
}

// hostLandingVenues is the Площадки the user represents, and the form that
// makes one.
func hostLandingVenues(data hostLandingData) *ui.Element {
	sect := []ui.Item{ui.Subhead(ui.Text("Площадки"))}
	if len(data.Venues) == 0 {
		sect = append(sect, ui.Empty(ui.Text("Площадок пока нет.")))
	} else {
		rows := make([]ui.Item, 0, len(data.Venues))
		for _, v := range data.Venues {
			row := []ui.Item{ui.Href(VenueBase(v)), ui.Listtitle(ui.Text(v.Title))}
			sub := v.City
			if !v.IsPublic {
				if sub != "" {
					sub += " · "
				}
				sub += "непубличная"
			}
			if sub != "" {
				row = append(row, ui.Muted(ui.Text(sub)))
			}
			rows = append(rows, ui.Listrow(row...))
		}
		sect = append(sect, ui.List(rows...))
	}
	return ui.Section(append(sect, venueCreateForm(data))...)
}

type profileData struct {
	HasPassword bool
	Username    string
	Telegram    string
	Timezone    string
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
	tzForm := []ui.Item{
		ui.ID("tzForm"), ui.DirCol, ui.Autocomplete("off"),
		ui.Textfield(ui.ID("tzValue"), ui.Name("timezone"), ui.Placeholder("Europe/Moscow"),
			ui.Value(data.Timezone), ui.Autocomplete("off"), ui.Maxlength("64")),
		ui.Hint(ui.Text("В этом поясе записывается время игр и голосований; календарь показывает его под сеткой.")),
		ui.Row(ui.Button(ui.Submit(), ui.Text("Сохранить"))),
	}
	page = append(page,
		ui.Section(
			ui.Hint(ui.Text(action)),
			ui.Form(form...),
			ui.Message(ui.ID("passwordMessage")),
		),
		ui.Section(
			ui.Hint(ui.Text("Часовой пояс")),
			ui.Form(tzForm...),
			ui.Message(ui.ID("tzMessage")),
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
	s.renderHostLandingForm(w, r, errMsg, "", nil)
}

// renderHostLandingForm re-renders /host after a refused create form, keeping
// what was typed and leaving that disclosure open.
func (s *Server) renderHostLandingForm(w http.ResponseWriter, r *http.Request, errMsg, open string, form url.Values) {
	user, ok := s.h.Engine().LookupSession(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	fests, err := s.loadHostFests(r.Context(), user.UserID)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	username := ""
	if user.Username.Valid {
		username = user.Username.String
	}
	if username == "" {
		username = dopestrings.Default.Host.Pages.UsernameFallback()
	}
	hostVenues, err := s.loadHostVenues(r.Context(), user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostLoggedInDoc(hostLandingData{
		LoggedIn: true,
		Username: username,
		Groups:   groupHostFests(fests, time.Now().Format("2006-01-02")),
		Venues:   hostVenues,
		Error:    errMsg,
		Form:     form,
		Open:     open,
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
		var hash, username, telegram, tz sql.NullString
		if err := s.h.Engine().DB.QueryRowContext(r.Context(),
			`select password_hash, username, telegram_username, timezone from users where id = ?`,
			user.UserID).Scan(&hash, &username, &telegram, &tz); err != nil {
			route.WriteError(w, r, err)
			return
		}
		pages.RenderDoc(w, s.h.Engine().AssetETags, profileDoc(profileData{
			HasPassword: hash.Valid && hash.String != "",
			Username:    username.String,
			Telegram:    strings.TrimPrefix(telegram.String, "@"),
			Timezone:    tz.String,
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
