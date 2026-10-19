package hostpages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"dope/dope/domain/games"
	"dope/dope/domain/multifest"
	"dope/dope/domain/numbering"
	"dope/dope/domain/view"
	"dope/dope/platform/roles"
	"dope/dope/platform/util"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/idstr"
	"pecheny.me/dopecore/session"

	"dope/dope/domain/core"
	"dope/dope/domain/festops"
	"dope/dope/web/route"
)

type hostFestDashData struct {
	Fest            view.HostFest
	Kind            string
	MultiFestVenues []venueRow
	Description     string
	Slug            string
	RatingID        int64
	Games           []PublicFestGame
	Access          []festaccess.HostAccessMember
	// RosterVenues are the venues whose teams a host limited to venues edits.
	RosterVenues []int64
	// HostGames lists, per limited host, the Games they may run, and
	// HostVenues the venues.
	HostGames, HostVenues map[int64][]int64
	TeamCount             int
	PlayerCount           int
	TroikaCount           int
	NumbersAssigned       int
	NumbersAllSet         bool
	CurrentRole           string
	CanManageFest         bool
	CanManageGames        bool
	CanManageAccess       bool
	CanDeleteFest         bool
	IsCreator             bool
	Error                 string
	AccessError           string
	AccessNotice          string
	Invites               []festInvite
	InviteError           string
	InviteNotice          string
	ImportError           string
	ImportNotice          string
	RosterError           string
	RosterNotice          string
}

type hostDashMessages struct {
	FormError    string
	AccessError  string
	AccessNotice string
	InviteError  string
	InviteNotice string
	ImportError  string
	ImportNotice string
	RosterError  string
	RosterNotice string
}

// hostFestDashDoc builds the fest dashboard: the (role-gated) fest-edit form, the
// games list with per-row settings/clear/delete controls, the access management
// section (bulk-action dialog + editable roster table), the participants links,
// and the delete-fest section. Confirms and the bulk dialog run through
// pageforms.js data-attributes (no inline on* handlers).
func hostFestDashDoc(data hostFestDashData) *dopeui.Doc {
	ref := data.Fest.Ref()
	s := dopestrings.Default
	page := []dopeui.Item{
		dopeui.Title(s.Host.Dash.PageTitle(data.Fest.Title)), dopeui.PagePublic, dopeui.Classicscripts("dist/pageforms.js"),
	}
	if data.Fest.IsPublic {
		page = append(page,
			dopeui.Data("jump-label", s.Host.Dash.JumpLabel()),
			dopeui.Data("jump-href", "/fest/"+ref),
			dopeui.Data("jump-title", s.Host.Dash.JumpTitle()),
			dopeui.Data("jump-icon", "eye"),
		)
	}
	page = append(page, dopeui.Publictopbar(pages.Trail(pages.HostCrumbs(), data.Fest.Title)))
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}
	if data.CanManageFest {
		page = append(page, hostDashFestForm(data, ref))
	}
	if len(data.MultiFestVenues) > 0 {
		page = append(page, hostDashVenuesSection(data, ref))
	}
	if data.CanManageGames || slices.ContainsFunc(data.Games, func(g PublicFestGame) bool { return g.MultiFestVenueID == 0 }) {
		page = append(page, hostDashGamesSection(data, ref))
	}
	if data.CanManageAccess {
		page = append(page, hostDashAccessSection(data, ref))
		if len(data.Games)+len(data.MultiFestVenues) > 0 {
			page = append(page, hostDashInvitesSection(data, ref))
		}
	}
	if data.CanManageFest {
		page = append(page, hostDashRosterSection(data, ref))
	} else if len(data.RosterVenues) > 0 {
		page = append(page, hostDashVenueRosterSection(data, ref))
	}
	if data.CanDeleteFest {
		page = append(page, dopeui.Section(
			dopeui.Subhead(dopeui.Text(s.Host.Dash.DeleteSubhead())),
			dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/delete"), dopeui.Autocomplete("off"),
				dopeui.Data("confirm", s.Host.Dash.DeleteConfirm()),
				dopeui.Note(dopeui.Text(s.Host.Dash.DeleteNote())),
				dopeui.Row(dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Text(s.Host.Dash.DeleteSubmit()))),
			),
		))
	}
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

func hostDashFestForm(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	ratingID := ""
	if data.RatingID != 0 {
		ratingID = idstr.Format(data.RatingID)
	}
	pub := dopeui.Checkbox(dopeui.Name("is_public"), dopeui.Value("1"), dopeui.Text(s.Host.Dash.PublicLabel()))
	if data.Fest.IsPublic {
		pub = dopeui.Checkbox(dopeui.Name("is_public"), dopeui.Value("1"), dopeui.Checked(), dopeui.Text(s.Host.Dash.PublicLabel()))
	}
	return dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref), dopeui.Autocomplete("off"),
		dopeui.Field(dopeui.Label(s.Host.Dash.TitleLabel()), dopeui.Textfield(dopeui.Name("title"), dopeui.Value(data.Fest.Title), dopeui.Required())),
		dopeui.Field(dopeui.Label(s.Host.Dash.DescriptionLabel()), dopeui.Editor(dopeui.Name("description"), dopeui.Rows("6"), dopeui.Text(data.Description))),
		dopeui.Field(dopeui.Label(s.Host.Dash.SlugLabel()),
			dopeui.Textfield(dopeui.Name("slug"), dopeui.Value(data.Slug), dopeui.Pattern("[a-z0-9-]+"), dopeui.Placeholder("my-fest"))),
		dopeui.Field(dopeui.Label(s.Host.Dash.StartDateLabel()), dopeui.Textfield(dopeui.Name("start_date"), dopeui.Value(data.Fest.StartDate))),
		dopeui.Field(dopeui.Label(s.Host.Dash.EndDateLabel()), dopeui.Textfield(dopeui.Name("end_date"), dopeui.Value(data.Fest.EndDate))),
		dopeui.Field(dopeui.Label("rating.chgk.info ID"), dopeui.Textfield(dopeui.Name("rating_id"), dopeui.Value(ratingID), dopeui.Inputmode("numeric"))),
		kindField(data.Kind),
		pub,
		dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Dash.SaveSubmit()))),
	)
}

// hostDashGameRow links to one game and, for a host who manages games, offers
// its settings, clear and delete. The note under the title is the game's slug
// unless the caller gives another.
func hostDashGameRow(g PublicFestGame, ref string, canManage bool, note string) *dopeui.Element {
	s := dopestrings.Default
	base := "/host/fest/" + ref + "/game/" + g.Ref()
	link := []dopeui.Item{dopeui.Href(base + "/"), dopeui.Listtitle(dopeui.Text(g.Title))}
	if note == "" {
		note = g.Slug
	}
	if note != "" {
		link = append(link, dopeui.Muted(dopeui.Text(note)))
	}
	row := []dopeui.Item{dopeui.Rowlink(link...)}
	if canManage {
		row = append(row,
			dopeui.Button(dopeui.Href(base+"/settings"), dopeui.Text(s.Host.Dash.SettingsBtn())),
			dopeui.Form(dopeui.Method("post"), dopeui.Action(base+"/clear"),
				dopeui.Data("confirm", s.Host.Dash.ClearConfirm()),
				dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Text(s.Host.Dash.ClearBtn()))),
			dopeui.Form(dopeui.Method("post"), dopeui.Action(base+"/delete"),
				dopeui.Data("confirm", s.Host.Dash.DeleteGameConfirm()),
				dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Text(s.Host.Dash.DeleteBtn()))),
		)
	}
	return dopeui.Actionrow(row...)
}

func hostDashGamesSection(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	sect := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.Dash.GamesSubhead()))}
	if len(data.Games) > 0 {
		rows := make([]dopeui.Item, 0, len(data.Games))
		for _, g := range data.Games {
			// A venue's Game is listed under its venue.
			if g.MultiFestVenueID == 0 {
				rows = append(rows, hostDashGameRow(g, ref, data.CanManageGames, ""))
			}
		}
		sect = append(sect, dopeui.Actionlist(rows...))
	} else {
		sect = append(sect, dopeui.Empty(dopeui.Text(s.Host.Dash.GamesEmpty())))
	}
	if data.CanManageGames {
		sect = append(sect, dopeui.Row(dopeui.Button(dopeui.Href("/host/fest/"+ref+"/game/new"), dopeui.Text(s.Host.Dash.AddGameBtn()))))
	}
	return dopeui.Section(sect...)
}

func hostDashAccessSection(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	sect := []dopeui.Item{dopeui.ID("access"), dopeui.Subhead(dopeui.Text(s.Host.Dash.AccessSubhead()))}
	if data.AccessError != "" {
		sect = append(sect, dopeui.Empty(dopeui.Text(data.AccessError)))
	}
	if data.AccessNotice != "" {
		sect = append(sect, dopeui.Note(dopeui.Text(data.AccessNotice)))
	}
	sect = append(sect,
		dopeui.Row(dopeui.Button(dopeui.Data("dialog-open", "bulkAccessDialog"), dopeui.Text(s.Host.Dash.BulkLabel()))),
		dopeui.Dialog(dopeui.ID("bulkAccessDialog"),
			dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/access#access"), dopeui.Autocomplete("off"),
				dopeui.Subhead(dopeui.Text(s.Host.Dash.BulkLabel())),
				dopeui.Hiddenfield(dopeui.Name("bulk_access"), dopeui.Value("1")),
				dopeui.Field(dopeui.Label(s.Host.Dash.BulkDataLabel()),
					dopeui.Editor(dopeui.Name("bulk_access_lines"), dopeui.Rows("8"),
						dopeui.Placeholder("username1:host\nusername2:host\nusername3:admin\nusername4:remove"), dopeui.Required())),
				dopeui.Row(dopeui.SpaceSM, dopeui.Wrap(),
					dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Dash.BulkApply())),
					dopeui.Button(dopeui.Data("dialog-close", ""), dopeui.Text(s.Host.Dash.CancelBtn())),
				),
			),
		),
	)

	rows := []dopeui.Item{dopeui.Trow(
		dopeui.Hcell(dopeui.Text(s.Host.Dash.ColNickname())), dopeui.Hcell(dopeui.Text(s.Host.Dash.ColRole())), dopeui.Hcell(),
	)}
	for _, m := range data.Access {
		uid := idstr.Format(m.UserID)
		var roleCell, actionCell *dopeui.Element
		if m.IsCreator {
			roleCell = dopeui.Cell(dopeui.Hiddenfield(dopeui.Name("role_"+uid), dopeui.Value("creator")), dopeui.Text("creator"))
			actionCell = dopeui.Cell()
		} else {
			roleCell = dopeui.Cell(dopeui.Selectfield(dopeui.Name("role_"+uid), dopeui.Data("autosubmit", ""),
				roleOption("admin", m.Role), roleOption("host", m.Role)))
			actionCell = dopeui.Cell(dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Name("delete_"+uid), dopeui.Value("1"),
				dopeui.Data("confirm", s.Host.Dash.DeleteAccessConfirm(m.Nickname)), dopeui.Text(s.Host.Dash.DeleteBtn())))
		}
		// A member's Games sit under their name rather than in a column of
		// their own: a fourth column squeezed the role select on a phone.
		rows = append(rows, dopeui.Trow(dopeui.Cell(dopeui.Col(dopeui.Paragraph(dopeui.Text(m.Nickname)), hostGames(data, m))), roleCell, actionCell))
	}
	rows = append(rows, dopeui.Trow(
		dopeui.Cell(dopeui.Textfield(dopeui.Name("new_nickname"), dopeui.Placeholder("nickname"))),
		dopeui.Cell(dopeui.Selectfield(dopeui.Name("new_role"),
			dopeui.Option(dopeui.Value("host"), dopeui.Text("host")),
			dopeui.Option(dopeui.Value("admin"), dopeui.Text("admin")))),
		dopeui.Cell(dopeui.Button(dopeui.Submit(), dopeui.Name("add_access"), dopeui.Value("1"), dopeui.Text(s.Host.Dash.AddBtn()))),
	))
	sect = append(sect,
		dopeui.Form(dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/access#access"), dopeui.Autocomplete("off"),
			dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...),
		),
	)
	return dopeui.Section(sect...)
}

// hostGames is a member's Games and venues: an admin or the creator runs all
// of them; a host gets a box per venue and per Game. Ticking some limits them
// to those, ticking none leaves them every Game.
func hostGames(data hostFestDashData, m festaccess.HostAccessMember) *dopeui.Element {
	s := dopestrings.Default
	if m.Role != roles.Host || len(data.Games)+len(data.MultiFestVenues) == 0 {
		return dopeui.Col()
	}
	uid := idstr.Format(m.UserID)
	boxes := []dopeui.Item{dopeui.Hiddenfield(dopeui.Name("games_present_"+uid), dopeui.Value("1"))}
	var summary []string
	group := func(field, label string, chosen []int64, ids []int64, titles []string) {
		ticked := map[int64]bool{}
		for _, id := range chosen {
			ticked[id] = true
		}
		var names []string
		for i, id := range ids {
			items := []dopeui.Item{dopeui.Name(field + uid), dopeui.Value(idstr.Format(id)), dopeui.Text(titles[i])}
			if ticked[id] {
				items = append(items, dopeui.Checked())
				names = append(names, titles[i])
			}
			boxes = append(boxes, dopeui.Checkbox(items...))
		}
		if len(names) > 0 {
			summary = append(summary, label+": "+strings.Join(names, ", "))
		}
	}
	if len(data.MultiFestVenues) > 0 {
		ids, titles := make([]int64, len(data.MultiFestVenues)), make([]string, len(data.MultiFestVenues))
		for i, v := range data.MultiFestVenues {
			ids[i], titles[i] = v.ID, v.Title
		}
		boxes = append(boxes, dopeui.Hint(dopeui.Text(s.Host.Dash.VenuesHint())))
		group("venues_", s.Host.Dash.ColVenues(), data.HostVenues[m.UserID], ids, titles)
	}
	ids, titles := make([]int64, len(data.Games)), make([]string, len(data.Games))
	for i, g := range data.Games {
		ids[i], titles[i] = g.ID, g.Title
	}
	group("games_", s.Host.Dash.ColGames(), data.HostGames[m.UserID], ids, titles)
	if len(summary) == 0 {
		summary = []string{s.Host.Dash.ColGames() + ": " + s.Host.Dash.GamesEvery()}
		boxes = append(boxes, dopeui.Hint(dopeui.Text(s.Host.Dash.GamesNoneHint())))
	}
	// Folded to what the host runs, so the boxes do not squeeze the role
	// beside them on a phone; opened, a box per venue and Game and one save
	// for all the boxes ticked.
	boxes = append(boxes, dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Dash.SaveSubmit()))))
	return dopeui.Details(dopeui.Summary(dopeui.Text(strings.Join(summary, "; "))), dopeui.Col(boxes...))
}

func roleOption(value, current string) *dopeui.Element {
	if value == current {
		return dopeui.Option(dopeui.Value(value), dopeui.Selected(), dopeui.Text(value))
	}
	return dopeui.Option(dopeui.Value(value), dopeui.Text(value))
}

func hostDashRosterSection(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	sect := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.Dash.RosterSubhead()))}
	if data.RosterError != "" {
		sect = append(sect, dopeui.Empty(dopeui.Text(data.RosterError)))
	}
	if data.RosterNotice != "" {
		sect = append(sect, dopeui.Note(dopeui.Text(data.RosterNotice)))
	}
	rows := []dopeui.Item{
		dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/teams"), dopeui.Listtitle(dopeui.Text(s.Host.Dash.RosterTeamsLink())), dopeui.Muted(dopeui.Text(strconv.Itoa(data.TeamCount)))),
		dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/players"), dopeui.Listtitle(dopeui.Text(s.Host.Dash.RosterPlayersLink())), dopeui.Muted(dopeui.Text(strconv.Itoa(data.PlayerCount)))),
		dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/troikas"), dopeui.Listtitle(dopeui.Text(s.Host.Dash.RosterTroikasLink())), dopeui.Muted(dopeui.Text(strconv.Itoa(data.TroikaCount)))),
	}
	if data.TeamCount > 0 {
		status := s.Host.Dash.NumbersStatusUnset()
		if data.NumbersAllSet {
			status = s.Host.Dash.NumbersStatusDone()
		} else if data.NumbersAssigned > 0 {
			status = s.Host.Dash.NumbersStatusPartial(strconv.Itoa(data.NumbersAssigned), strconv.Itoa(data.TeamCount))
		}
		rows = append(rows, dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/numbers"),
			dopeui.Listtitle(dopeui.Text(s.Host.Dash.NumbersLink())), dopeui.Muted(dopeui.Text(status))))
	}
	ratingStatus := s.Host.Dash.RatingStatusNone()
	if data.RatingID != 0 {
		ratingStatus = "rating " + idstr.Format(data.RatingID)
	}
	rows = append(rows,
		dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/rating/import"),
			dopeui.Listtitle(dopeui.Text(s.Host.Dash.RosterImportLink())), dopeui.Muted(dopeui.Text(ratingStatus))),
		dopeui.Listrow(dopeui.Href("/host/fest/"+ref+"/audit"),
			dopeui.Listtitle(dopeui.Text(s.Host.Dash.AuditLink())), dopeui.Muted(dopeui.Text(s.Host.Dash.AuditMuted()))),
	)
	sect = append(sect, dopeui.List(rows...))
	return dopeui.Section(sect...)
}

// FestSettings is a fest's editable header, the same fields the landing's
// create form and the dashboard's settings form post. RatingID 0 means none.
type FestSettings struct {
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	RatingID    int64  `json:"rating_id"`
	IsPublic    bool   `json:"is_public"`
	// Kind is the fest's type: regular, synch or multi (ADR-0031).
	Kind string `json:"kind"`
}

func festSettingsFromForm(form url.Values) FestSettings {
	rating, _ := util.ParseOptionalInt64(form.Get("rating_id")).(int64)
	return FestSettings{
		Title:       strings.TrimSpace(form.Get("title")),
		Slug:        strings.TrimSpace(form.Get("slug")),
		Description: form.Get("description"),
		StartDate:   strings.TrimSpace(form.Get("start_date")),
		EndDate:     strings.TrimSpace(form.Get("end_date")),
		RatingID:    rating,
		IsPublic:    form.Get("is_public") == "1",
		Kind:        form.Get("kind"),
	}
}

// kindValue is the fest's type, regular when none is given.
func (f FestSettings) kindValue() (string, error) {
	if f.Kind == "" {
		return multifest.Regular, nil
	}
	if !multifest.ValidKind(f.Kind) {
		return "", corei18n.User(dopestrings.Default.Host.Dash.ErrorKindInvalid())
	}
	return f.Kind, nil
}

func (f FestSettings) ratingValue() any {
	if f.RatingID > 0 {
		return f.RatingID
	}
	return nil
}

// CreateFest makes a fest with the user as its creator and returns its id.
// The landing's form sends no slug, and such a fest goes by its id until the
// dashboard gives it one; the API may name one at once.
func (s *Server) CreateFest(reqCtx context.Context, userID int64, f FestSettings) (int64, error) {
	f.Title = strings.TrimSpace(f.Title)
	if f.Title == "" {
		return 0, corei18n.User(dopestrings.Default.Host.Dash.ErrorTitleRequired())
	}
	kind, err := f.kindValue()
	if err != nil {
		return 0, err
	}
	var slugValue any
	if slug := strings.TrimSpace(f.Slug); slug != "" {
		if err := s.checkFestSlug(reqCtx, slug, 0); err != nil {
			return 0, err
		}
		slugValue = slug
	}
	var festID int64
	_, err = s.commit(reqCtx, 0, "fest-create", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		now := util.UtcNow()
		var err error
		festID, err = store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, start_date, end_date, is_public, kind)
values(?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)`,
			slugValue, f.Title, f.Description, f.ratingValue(), userID, now, now,
			util.NullableString(strings.TrimSpace(f.StartDate)), util.NullableString(strings.TrimSpace(f.EndDate)), util.BoolToInt(f.IsPublic), kind)
		if util.IsUniqueViolation(err) {
			// Another fest took the slug between the check and the insert.
			return core.FestWrite{}, corei18n.User(dopestrings.Default.Host.Dash.ErrorSlugTaken())
		}
		if err != nil {
			return core.FestWrite{}, err
		}
		_, err = tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'creator', ?)`, festID, userID, now)
		return core.FestWrite{}, err
	})
	return festID, err
}

func (s *Server) handleHostCreateFest(w http.ResponseWriter, r *http.Request, user session.User) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	festID, err := s.CreateFest(r.Context(), user.UserID, festSettingsFromForm(r.Form))
	if msg, ok := corei18n.AsUser(err); ok {
		s.renderHostLanding(w, r, msg)
		return
	}
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%d", festID), http.StatusSeeOther)
}

// LoadFestSettings reads a fest's header as the settings form shows it.
func (s *Server) LoadFestSettings(ctx context.Context, festID int64) (FestSettings, error) {
	var (
		f         FestSettings
		startDate sql.NullString
		endDate   sql.NullString
		ratingID  sql.NullInt64
		isPublic  int
	)
	err := s.h.Engine().DB.QueryRowContext(ctx, `
select title, coalesce(slug, ''), description, start_date, end_date, rating_id, is_public, kind
from fests where id = ?`, festID).Scan(&f.Title, &f.Slug, &f.Description, &startDate, &endDate, &ratingID, &isPublic, &f.Kind)
	f.StartDate, f.EndDate, f.RatingID, f.IsPublic = startDate.String, endDate.String, ratingID.Int64, isPublic == 1
	return f, err
}

// UpdateFest writes a fest's whole header. An empty slug clears it. The type
// changes only while the fest has no Games, which its venues would split.
func (s *Server) UpdateFest(reqCtx context.Context, festID int64, f FestSettings) error {
	f.Title = strings.TrimSpace(f.Title)
	if f.Title == "" {
		return corei18n.User(dopestrings.Default.Host.Dash.ErrorTitleRequired())
	}
	kind, err := f.kindValue()
	if err != nil {
		return err
	}
	slug := strings.TrimSpace(f.Slug)
	var slugValue any
	if slug != "" {
		if err := s.checkFestSlug(reqCtx, slug, festID); err != nil {
			return err
		}
		slugValue = slug
	}
	_, err = s.commit(reqCtx, festID, "fest-settings", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		var changed bool
		if err := tx.QueryRowContext(ctx, `
select kind <> ? and exists (select 1 from games where fest_id = fests.id) from fests where id = ?`, kind, festID).Scan(&changed); err != nil {
			return core.FestWrite{}, err
		}
		if changed {
			return core.FestWrite{}, corei18n.User(dopestrings.Default.Host.Dash.ErrorKindHasGames())
		}
		_, err := tx.ExecContext(ctx, `
update fests
set title = ?, slug = ?, description = ?, rating_id = ?, start_date = ?, end_date = ?, is_public = ?, kind = ?, updated_at = ?
where id = ?`,
			f.Title, slugValue, f.Description, f.ratingValue(),
			util.NullableString(strings.TrimSpace(f.StartDate)), util.NullableString(strings.TrimSpace(f.EndDate)), util.BoolToInt(f.IsPublic),
			kind, util.UtcNow(), festID)
		return core.FestWrite{}, err
	})
	return err
}

func (s *Server) handleHostUpdateFest(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f := festSettingsFromForm(r.Form)
	err := s.UpdateFest(r.Context(), festID, f)
	if msg, ok := corei18n.AsUser(err); ok {
		s.renderHostFestDashboard(w, r, festID, hostDashMessages{FormError: msg})
		return
	}
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	redirectRef := f.Slug
	if redirectRef == "" {
		redirectRef = fmt.Sprintf("%d", festID)
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s", redirectRef), http.StatusSeeOther)
}

// saveAccess runs one change to the fest's access through the commit step,
// recorded as fest:access.
func (s *Server) saveAccess(ctx context.Context, festID int64, write func(ctx context.Context, tx *sql.Tx) error) error {
	_, err := s.commit(ctx, festID, "fest-access", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		return core.FestWrite{Event: "fest:access"}, write(ctx, tx)
	})
	return err
}

func (s *Server) handleHostSaveAccess(w http.ResponseWriter, r *http.Request, festID, actorID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("bulk_access") == "1" {
		var count int
		err := s.saveAccess(r.Context(), festID, func(ctx context.Context, tx *sql.Tx) (err error) {
			count, err = festaccess.SaveFestAccessBulkTx(ctx, tx, festID, actorID, r.Form.Get("bulk_access_lines"))
			return err
		})
		if err != nil {
			s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessError: err.Error()})
			return
		}
		s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessNotice: dopestrings.Default.Host.Dash.BulkDoneNotice(strconv.Itoa(count))})
		return
	}
	if err := s.saveAccess(r.Context(), festID, func(ctx context.Context, tx *sql.Tx) error {
		return festaccess.SaveFestAccessTx(ctx, tx, festID, actorID, r.Form)
	}); err != nil {
		s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessError: err.Error()})
		return
	}
	s.renderHostFestDashboard(w, r, festID, hostDashMessages{AccessNotice: dopestrings.Default.Host.Dash.AccessSavedNotice()})
}

// checkFestSlug refuses a slug that is malformed or that another fest holds.
func (s *Server) checkFestSlug(ctx context.Context, slug string, festID int64) error {
	if err := util.ValidateSlug(slug); err != nil {
		return corei18n.User(dopestrings.Default.Host.Dash.ErrorSlugInvalid(err.Error()))
	}
	if taken, err := s.slugTakenByOtherFest(ctx, slug, festID); err != nil {
		return err
	} else if taken {
		return corei18n.User(dopestrings.Default.Host.Dash.ErrorSlugTaken())
	}
	return nil
}

func (s *Server) slugTakenByOtherFest(ctx context.Context, slug string, festID int64) (bool, error) {
	var count int
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select count(*) from fests where slug = ? and id <> ?`, slug, festID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Server) festRefOrID(ctx context.Context, festID int64) string {
	var slug string
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select coalesce(slug, '') from fests where id = ?`, festID).Scan(&slug); err == nil && slug != "" {
		return slug
	}
	return fmt.Sprintf("%d", festID)
}

func (s *Server) gameRefOrID(ctx context.Context, gameID int64) string {
	var slug string
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select coalesce(slug, '') from games where id = ?`, gameID).Scan(&slug); err == nil && slug != "" {
		return slug
	}
	return fmt.Sprintf("%d", gameID)
}

// DeleteFest deletes the fest and everything in it; only its creator may.
func (s *Server) DeleteFest(ctx context.Context, festID, userID int64) error {
	creator, err := s.isFestCreator(ctx, festID, userID)
	if err != nil {
		return err
	}
	if !creator {
		return route.Forbid(dopestrings.Default.Host.Dash.ErrorDeleteCreatorOnly())
	}
	_, err = s.commit(ctx, festID, "fest-delete", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		return festops.DeleteFestTx(ctx, tx, festID, userID)
	})
	return err
}

func (s *Server) handleHostDeleteFest(w http.ResponseWriter, r *http.Request, festID, userID int64) {
	if err := s.DeleteFest(r.Context(), festID, userID); err != nil {
		route.WriteError(w, r, err)
		return
	}
	http.Redirect(w, r, "/host", http.StatusSeeOther)
}

func (s *Server) renderHostFestDashboard(w http.ResponseWriter, r *http.Request, festID int64, msgs hostDashMessages) {
	var (
		title       string
		slug        string
		description string
		startDate   sql.NullString
		endDate     sql.NullString
		ratingID    sql.NullInt64
		isPublic    int
		kind        string
	)
	if err := s.h.Engine().DB.QueryRowContext(r.Context(), `
select title, coalesce(slug, ''), description, start_date, end_date, rating_id, is_public, kind
from fests where id = ?`, festID).Scan(&title, &slug, &description, &startDate, &endDate, &ratingID, &isPublic, &kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		route.WriteError(w, r, err)
		return
	}
	gameRows, err := LoadFestGames(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	festRef := slug
	if festRef == "" {
		festRef = fmt.Sprintf("%d", festID)
	}
	currentRole, userID := "", int64(0)
	if user, ok := s.h.Engine().LookupSession(r); ok {
		userID = user.UserID
		currentRole, err = festaccess.FestUserRoleFromQuery(r.Context(), s.h.Engine().DB, festID, userID)
		if err != nil {
			route.WriteError(w, r, err)
			return
		}
	}
	hostGames := make([]PublicFestGame, 0, len(gameRows))
	for _, g := range gameRows {
		// A host an admin limited to some Games sees those alone.
		if may, err := festaccess.MayRunGame(r.Context(), s.h.Engine().DB, festID, g.ID, userID, currentRole); err != nil {
			route.WriteError(w, r, err)
			return
		} else if !may && roles.CanEditGameTables(currentRole) {
			continue
		}
		hostGames = append(hostGames, PublicFestGame{
			ID:               g.ID,
			Slug:             g.Slug,
			Code:             g.Code,
			Title:            g.Title,
			Type:             games.Label(g.Type),
			URL:              fmt.Sprintf("/host/fest/%s/game/%s/", festRef, g.Ref()),
			MultiFestVenueID: g.MultiFestVenueID,
		})
	}
	teamCount, playerCount, troikaCount, err := s.loadHostFestRosterCounts(r.Context(), festID)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	var numbersAssigned int
	if err := s.h.Engine().DB.QueryRowContext(r.Context(), `
select coalesce(sum(case when `+numbering.Numbered+` then 1 else 0 end), 0)
from fest_teams where fest_id = ? and deleted = 0`, festID).Scan(&numbersAssigned); err != nil {
		route.WriteError(w, r, err)
		return
	}
	canManageFest := roles.CanManageFest(currentRole)
	canManageAccess := roles.CanManageAccess(currentRole)
	canDeleteFest := roles.CanDeleteFest(currentRole)
	canManageGames := canManageFest
	var access []festaccess.HostAccessMember
	var hostGameIDs, hostVenueIDs map[int64][]int64
	var invites []festInvite
	if canManageAccess {
		if invites, err = s.loadFestInvites(r.Context(), festID, pages.PublicOrigin(r)); err != nil {
			route.WriteError(w, r, err)
			return
		}
		access, err = festaccess.LoadFestAccessMembers(s.h.Engine(), r.Context(), festID)
		if err != nil {
			route.WriteError(w, r, err)
			return
		}
		if hostGameIDs, err = festaccess.HostGamesByUser(r.Context(), s.h.Engine().DB, festID); err != nil {
			route.WriteError(w, r, err)
			return
		}
		if hostVenueIDs, err = festaccess.HostVenuesByUser(r.Context(), s.h.Engine().DB, festID); err != nil {
			route.WriteError(w, r, err)
			return
		}
	}
	venueRows, err := s.loadVenueRows(r.Context(), festID, hostGames)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	rosterVenues, _, err := festaccess.RosterVenues(r.Context(), s.h.Engine().DB, festID, userID, currentRole)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	if !canManageFest {
		venueRows = ownVenueRows(venueRows, rosterVenues)
	}
	data := hostFestDashData{
		MultiFestVenues: venueRows,
		RosterVenues:    rosterVenues,
		Fest: view.HostFest{
			ID:        festID,
			Slug:      slug,
			Title:     title,
			StartDate: startDate.String,
			EndDate:   endDate.String,
			Dates:     util.FormatFestDates(startDate.String, endDate.String),
			IsPublic:  isPublic == 1,
		},
		Kind:            kind,
		Description:     description,
		Slug:            slug,
		Games:           hostGames,
		Access:          access,
		HostGames:       hostGameIDs,
		HostVenues:      hostVenueIDs,
		TeamCount:       teamCount,
		PlayerCount:     playerCount,
		TroikaCount:     troikaCount,
		NumbersAssigned: numbersAssigned,
		NumbersAllSet:   teamCount > 0 && numbersAssigned == teamCount,
		CurrentRole:     currentRole,
		CanManageFest:   canManageFest,
		CanManageGames:  canManageGames,
		CanManageAccess: canManageAccess,
		CanDeleteFest:   canDeleteFest,
		IsCreator:       canDeleteFest,
		Error:           msgs.FormError,
		AccessError:     msgs.AccessError,
		AccessNotice:    msgs.AccessNotice,
		Invites:         invites,
		InviteError:     msgs.InviteError,
		InviteNotice:    msgs.InviteNotice,
		ImportError:     msgs.ImportError,
		ImportNotice:    msgs.ImportNotice,
		RosterError:     msgs.RosterError,
		RosterNotice:    msgs.RosterNotice,
	}
	if ratingID.Valid {
		data.RatingID = ratingID.Int64
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostFestDashDoc(data))
}

func (s *Server) loadHostFestRosterCounts(ctx context.Context, festID int64) (int, int, int, error) {
	var teamCount, playerCount, troikaCount int
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select count(*) from fest_teams where fest_id = ? and deleted = 0`, festID).Scan(&teamCount); err != nil {
		return 0, 0, 0, err
	}
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select count(*) from fest_players where fest_id = ?`, festID).Scan(&playerCount); err != nil {
		return 0, 0, 0, err
	}
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select count(*) from participants where fest_id = ? and assembled = 1`, festID).Scan(&troikaCount); err != nil {
		return 0, 0, 0, err
	}
	return teamCount, playerCount, troikaCount, nil
}

func (s *Server) loadFestRatingID(ctx context.Context, festID int64) (int64, error) {
	var ratingID sql.NullInt64
	if err := s.h.Engine().DB.QueryRowContext(ctx, `select rating_id from fests where id = ?`, festID).Scan(&ratingID); err != nil {
		return 0, err
	}
	if !ratingID.Valid {
		return 0, nil
	}
	return ratingID.Int64, nil
}

// loadHostFests lists the fests the user organises. The site admin gets every
// fest, since festaccess gives them a role on each.
func (s *Server) loadHostFests(ctx context.Context, user session.User) ([]view.HostFest, error) {
	all := user.Username.Valid && festaccess.IsSiteAdmin(user.Username.String)
	return store.CollectRows(ctx, s.h.Engine().DB, `
select t.id, coalesce(t.slug, ''), t.title, coalesce(t.start_date, ''), coalesce(t.end_date, ''), t.is_public
from fests t
where ? or exists(select 1 from fest_organizers o where o.fest_id = t.id and o.user_id = ?)
order by case when t.start_date is null or t.start_date = '' then 1 else 0 end,
         t.start_date desc,
         t.id desc`, []any{all, user.UserID}, func(rows *sql.Rows) (view.HostFest, error) {
		var t view.HostFest
		var pub int
		if err := rows.Scan(&t.ID, &t.Slug, &t.Title, &t.StartDate, &t.EndDate, &pub); err != nil {
			return t, err
		}
		t.IsPublic = pub == 1
		t.Dates = util.HumanizeFestDates(t.StartDate, t.EndDate, time.Now().Year())
		return t, nil
	})
}

// hostFestGroup is one collapsible bucket on the host landing page.
type hostFestGroup struct {
	Title string
	Fests []view.HostFest
}

// groupHostFests partitions the host's fests into current/future/past buckets
// relative to today ("YYYY-MM-DD"), sorts each bucket by start date descending
// (then title ascending), and drops empty buckets.
func groupHostFests(fests []view.HostFest, today string) []hostFestGroup {
	s := dopestrings.Default
	var current, future, past []view.HostFest
	for _, f := range fests {
		switch util.ClassifyFestDate(f.StartDate, f.EndDate, today) {
		case util.FestCurrent:
			current = append(current, f)
		case util.FestFuture:
			future = append(future, f)
		default:
			past = append(past, f)
		}
	}
	sortHostFests(current)
	sortHostFests(future)
	sortHostFests(past)
	all := []hostFestGroup{
		{Title: s.Host.Pages.GroupCurrent(), Fests: current},
		{Title: s.Host.Pages.GroupFuture(), Fests: future},
		{Title: s.Host.Pages.GroupPast(), Fests: past},
	}
	groups := make([]hostFestGroup, 0, len(all))
	for _, g := range all {
		if len(g.Fests) > 0 {
			groups = append(groups, g)
		}
	}
	return groups
}

func sortHostFests(fests []view.HostFest) {
	sort.SliceStable(fests, func(i, j int) bool {
		if fests[i].StartDate != fests[j].StartDate {
			return fests[i].StartDate > fests[j].StartDate // descending
		}
		return fests[i].Title < fests[j].Title
	})
}

func (s *Server) isFestCreator(ctx context.Context, festID, userID int64) (bool, error) {
	var n int
	err := s.h.Engine().DB.QueryRowContext(ctx, `
select count(*) from fests where id = ? and created_by = ?`, festID, userID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
