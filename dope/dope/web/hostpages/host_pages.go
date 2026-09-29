package hostpages

import (
	"database/sql"
	"dope/dope/domain/core"
	"dope/dope/web/pages"
	"dope/dope/web/route"
	ui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
	"errors"
	"fmt"
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
		ui.Data("jump-icon", "eye"),
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
	Tokens      []core.APIToken
	// NewToken is the raw token just made, shown this once.
	NewToken string
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
	page := []ui.Item{ui.Title(s.Host.Pages.ProfileTitle()), ui.PagePublic, ui.Classicscripts("dist/profile.js dist/pageforms.js"),
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
		tokensSection(data),
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
	fests, err := s.loadHostFests(r.Context(), user)
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
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostLoggedInDoc(hostLandingData{
		LoggedIn: true,
		Username: username,
		Groups:   groupHostFests(fests, time.Now().Format("2006-01-02")),
		Error:    errMsg,
	}))
}

// tokensSection lists the account's API tokens (ADR-0021) with a revoke
// button on each live one, and the form that makes a new one. A token just
// made is shown above the list, the only time it can be read.
func tokensSection(data profileData) *ui.Element {
	s := dopestrings.Default
	sect := []ui.Item{ui.ID("tokens"), ui.Subhead(ui.Text(s.Host.Pages.TokensSubhead())), ui.Hint(ui.Text(s.Host.Pages.TokensLead()))}
	if data.NewToken != "" {
		sect = append(sect, ui.Field(ui.Label(s.Host.Pages.TokenNewLabel()),
			ui.Editor(ui.Rows("2"), ui.Readonly(), ui.Data("select-all", ""), ui.Text(data.NewToken)),
		), ui.Hint(ui.HintDanger, ui.Text(s.Host.Pages.TokenNewHint())))
	}
	if len(data.Tokens) > 0 {
		rows := make([]ui.Item, 0, len(data.Tokens))
		for _, t := range data.Tokens {
			label := t.Label
			if label == "" {
				label = s.Host.Pages.TokenUnnamed()
			}
			var state string
			switch {
			case t.RevokedAt != nil:
				state = s.Host.Pages.TokenRevoked(tokenDate(*t.RevokedAt))
			case !t.Active:
				state = s.Host.Pages.TokenExpired()
			case t.LastUsedAt != nil:
				state = s.Host.Pages.TokenUsed(tokenDate(*t.LastUsedAt))
			default:
				state = s.Host.Pages.TokenNeverUsed()
			}
			row := []ui.Item{ui.Col(
				ui.Listtitle(ui.Text(label)),
				ui.Muted(ui.Text(s.Host.Pages.TokenMeta(tokenDate(t.CreatedAt), tokenDate(t.ExpiresAt), state))),
			)}
			if t.Active {
				row = append(row, ui.Form(ui.Method("post"), ui.Action(fmt.Sprintf("/profile/tokens/%d/revoke", t.ID)),
					ui.Data("confirm", s.Host.Pages.TokenRevokeConfirm()),
					ui.Button(ui.Danger, ui.Submit(), ui.Text(s.Host.Pages.TokenRevoke()))))
			}
			rows = append(rows, ui.Listrow(row...))
		}
		sect = append(sect, ui.List(rows...))
	} else {
		sect = append(sect, ui.Empty(ui.Text(s.Host.Pages.TokensEmpty())))
	}
	sect = append(sect, ui.Form(ui.DirCol, ui.Method("post"), ui.Action("/profile/tokens"), ui.Autocomplete("off"),
		ui.Field(ui.Label(s.Host.Pages.TokenLabelLabel()),
			ui.Textfield(ui.Name("label"), ui.Maxlength(strconv.Itoa(core.APITokenLabelMax)), ui.Placeholder(s.Host.Pages.TokenLabelPlaceholder()))),
		ui.Row(ui.Button(ui.Submit(), ui.Text(s.Host.Pages.TokenCreateSubmit()))),
	))
	return ui.Section(sect...)
}

// tokenDate shows an RFC 3339 stamp as the day it names.
func tokenDate(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Format("2006-01-02")
	}
	return ts
}

func (s *Server) renderProfile(w http.ResponseWriter, r *http.Request, user session.User, newToken string) {
	var hash, username, telegram sql.NullString
	if err := s.h.Engine().DB.QueryRowContext(r.Context(),
		`select password_hash, username, telegram_username from users where id = ?`,
		user.UserID).Scan(&hash, &username, &telegram); err != nil {
		route.WriteError(w, r, err)
		return
	}
	tokens, err := s.h.Engine().ListAPITokens(r.Context(), user.UserID)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, profileDoc(profileData{
		HasPassword: hash.Valid && hash.String != "",
		Username:    username.String,
		Telegram:    strings.TrimPrefix(telegram.String, "@"),
		Tokens:      tokens,
		NewToken:    newToken,
	}))
}

// HandleProfileTokens serves POST /profile/tokens, which makes a token and
// shows it once, and POST /profile/tokens/{id}/revoke.
func (s *Server) HandleProfileTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !route.SameOriginUnsafe(w, r) {
		return
	}
	user, ok := s.h.Engine().LookupCookieSession(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if r.URL.Path == "/profile/tokens" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		created, err := s.h.Engine().CreateAPIToken(r.Context(), user.UserID, r.Form.Get("label"))
		if err != nil {
			route.WriteError(w, r, err)
			return
		}
		s.renderProfile(w, r, user, created.Token)
		return
	}
	idText, found := strings.CutPrefix(r.URL.Path, "/profile/tokens/")
	idText, isRevoke := strings.CutSuffix(idText, "/revoke")
	id, err := strconv.ParseInt(idText, 10, 64)
	if !found || !isRevoke || err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := s.h.Engine().RevokeAPIToken(r.Context(), user.UserID, id); err != nil && !errors.Is(err, core.ErrNoAPIToken) {
		route.WriteError(w, r, err)
		return
	}
	http.Redirect(w, r, "/profile#tokens", http.StatusSeeOther)
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
		s.renderProfile(w, r, user, "")
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
