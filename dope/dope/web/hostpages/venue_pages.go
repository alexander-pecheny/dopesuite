package hostpages

import (
	"html/template"
	"strconv"
	"strings"

	"dope/dope/domain/venues"
	"dope/dope/web/pages"
	ui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
)

type VenueRow struct {
	Ref           string
	Title         string
	City          string
	NextSlot      string
	Registration  string
	Accepted      int
	RatingVenueID int64
}

type SlotRow struct {
	Date         string
	Tournament   string
	Registration string
	RegHref      string
	Accepted     int
}

// MineHereRow is one of the reader's own games at this Venue: what they played
// or signed up for, and the way back to either.
type MineHereRow struct {
	Date     string
	Team     string
	Status   string
	RegToken string
	GameHref string
}

type VenueDetail struct {
	Ref           string
	Title         string
	City          string
	RatingVenueID int64
	Description   template.HTML
	Upcoming      []SlotRow
	Past          []SlotRow
	Mine          []MineHereRow
	IsHost        bool
}

type RegPage struct {
	Token       string
	VenueTitle  string
	VenueRef    string
	City        string
	Date        string
	Tournament  string
	State       venues.RegState
	OpensAt     string
	LoggedIn    bool
	LoginHref   string
	Application *ApplicationView
	GameHref    string
	Error       string
	Notice      string
}

type ApplicationView struct {
	Status       string
	StatusLabel  string
	TeamName     string
	Alias        string
	RealTeamName string
	RatingTeamID int64
	Roster       []venues.RosterPlayer
	Flags        []string
	Number       int64
}

func joinDots(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

func ratingVenueLink(id int64) ui.Item {
	return ui.Link(ui.Href("https://rating.chgk.info/venues/"+strconv.FormatInt(id, 10)), ui.Newtab(),
		ui.Text(strs.Venues.Public.RatingLink()))
}

// MineRow is one of the reader's own applications as the index lists it.
type MineRow struct {
	Date      string
	VenueRef  string
	VenueName string
	Team      string
	Status    string
	RegToken  string
	AppID     int64
}

func VenuesIndexDoc(rows []VenueRow, mine []MineRow, loggedIn bool) *ui.Doc {
	page := []ui.Item{ui.Title(strs.Venues.Public.IndexTitle()), ui.PagePublic, ui.Classicscripts("dist/pageforms.js")}
	page = append(page, jumpHostNav("/host", dopestrings.Default.Host.Pages.JumpHostLabel(), dopestrings.Default.Host.Pages.JumpHostTitleIndex())...)
	page = append(page, ui.Publictopbar(pages.Trail([]ui.Item{pages.HomeCrumb()}, strs.Venues.Public.IndexTitle())))
	page = append(page, PublicTabs("/venues"))
	if loggedIn {
		page = append(page, mineSection(mine))
	}
	if len(rows) == 0 {
		page = append(page, ui.Empty(ui.Text(strs.Venues.Public.IndexEmpty())))
		return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
	}
	page = append(page, ui.Field(ui.Label(strs.Venues.Public.SearchLabel()),
		ui.Textfield(ui.Data("filter-rows", "venues"), ui.Placeholder(strs.Venues.Public.SearchPlaceholder()), ui.Autocomplete("off"))))

	table := []ui.Item{ui.ID("venues"), ui.Scroll(), ui.Trow(
		ui.Hcell(ui.Text(strs.Venues.Public.ColVenue())), ui.Hcell(ui.Text(strs.Venues.Public.ColCity())), ui.Hcell(ui.Text(strs.Venues.Public.ColNextGame())),
		ui.Hcell(ui.Text(strs.Venues.Public.ColRegistration())), ui.Hcell(ui.Text(strs.Venues.Public.ColTeams())), ui.Hcell(ui.Text(strs.Venues.Public.ColRating())),
	)}
	for _, v := range rows {
		rating := ui.Cell(ui.Text(""))
		if v.RatingVenueID > 0 {
			rating = ui.Cell(ratingVenueLink(v.RatingVenueID))
		}
		table = append(table, ui.Trow(
			ui.Cell(ui.Link(ui.Href("/venue/"+v.Ref), ui.Text(v.Title))),
			ui.Cell(ui.Text(v.City)),
			ui.Cell(ui.Text(v.NextSlot)),
			ui.Cell(ui.Text(v.Registration)),
			ui.Cell(ui.Text(strconv.Itoa(v.Accepted))),
			rating,
		))
	}
	page = append(page, ui.Table(table...))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

// mineSection is every application the reader has out, at every Venue: they
// file at several and would otherwise have to keep the registration links to
// find them again. Each row leads back to its own form, or withdraws it.
func mineSection(rows []MineRow) *ui.Element {
	sect := []ui.Item{ui.Subhead(ui.Text(strs.Venues.Mine.Subhead()))}
	if len(rows) == 0 {
		return ui.Section(append(sect, ui.Empty(ui.Text(strs.Venues.Mine.Empty())))...)
	}
	table := []ui.Item{ui.Scroll(), ui.Trow(
		ui.Hcell(ui.Text(strs.Venues.Mine.ColGame())), ui.Hcell(ui.Text(strs.Venues.Mine.ColVenue())),
		ui.Hcell(ui.Text(strs.Venues.Mine.ColTeam())), ui.Hcell(ui.Text(strs.Venues.Mine.ColStatus())),
		ui.Hcell(ui.Text("")),
	)}
	for _, m := range rows {
		reg := "/reg/" + m.RegToken
		table = append(table, ui.Trow(
			ui.Cell(ui.Link(ui.Href(reg), ui.Text(venues.HumanDate(m.Date)))),
			ui.Cell(ui.Link(ui.Href("/venue/"+m.VenueRef), ui.Text(m.VenueName))),
			ui.Cell(ui.Text(m.Team)),
			ui.Cell(ui.Text(m.Status)),
			ui.Cell(ui.Row(ui.SpaceSM, ui.AlignCenter, ui.Wrap(),
				ui.Button(ui.Ghost, ui.Small(), ui.Href(reg), ui.Text(strs.Venues.Mine.EditBtn())),
				ui.Form(ui.Method("post"), ui.Action(reg+"/withdraw"),
					ui.Data("confirm", strs.Venues.Reg.WithdrawConfirm()),
					ui.Button(ui.Danger, ui.Small(), ui.Submit(), ui.Text(strs.Venues.Reg.WithdrawBtn()))))),
		))
	}
	return ui.Section(append(sect, ui.Table(table...))...)
}

func slotTable(title string, rows []SlotRow) *ui.Element {
	table := []ui.Item{ui.Scroll(), ui.Trow(
		ui.Hcell(ui.Text(strs.Venues.Public.ColWhen())), ui.Hcell(ui.Text(strs.Venues.Public.ColTournament())),
		ui.Hcell(ui.Text(strs.Venues.Public.ColRegistration())), ui.Hcell(ui.Text(strs.Venues.Public.ColTeams())),
	)}
	for _, s := range rows {
		reg := ui.Cell(ui.Text(s.Registration))
		if s.RegHref != "" {
			reg = ui.Cell(ui.Link(ui.Href(s.RegHref), ui.Text(s.Registration)))
		}
		table = append(table, ui.Trow(
			ui.Cell(ui.Text(s.Date)), ui.Cell(ui.Text(s.Tournament)),
			reg, ui.Cell(ui.Text(strconv.Itoa(s.Accepted))),
		))
	}
	return ui.Section(ui.Subhead(ui.Text(title)), ui.Table(table...))
}

// mineHereSection is what this reader has played here and signed up for next.
// It is only ever their own, so a page served to a stranger does not carry it.
func mineHereSection(rows []MineHereRow) *ui.Element {
	table := []ui.Item{ui.Scroll(), ui.Trow(
		ui.Hcell(ui.Text(strs.Venues.Mine.ColGame())), ui.Hcell(ui.Text(strs.Venues.Mine.ColTeam())),
		ui.Hcell(ui.Text(strs.Venues.Mine.ColStatus())), ui.Hcell(ui.Text("")),
	)}
	for _, m := range rows {
		// A game already played is a table to read; one still to come is an
		// application to edit.
		open := ui.Cell(ui.Text(""))
		switch {
		case m.GameHref != "":
			open = ui.Cell(ui.Link(ui.Href(m.GameHref), ui.Text(strs.Venues.Mine.OpenGame())))
		case m.RegToken != "":
			open = ui.Cell(ui.Link(ui.Href("/reg/"+m.RegToken), ui.Text(strs.Venues.Mine.EditBtn())))
		}
		table = append(table, ui.Trow(
			ui.Cell(ui.Text(venues.HumanDate(m.Date))), ui.Cell(ui.Text(m.Team)),
			ui.Cell(ui.Text(m.Status)), open,
		))
	}
	return ui.Section(ui.Subhead(ui.Text(strs.Venues.Mine.HereSubhead())), ui.Table(table...))
}

// venueHeadLine is the one line under a Venue's name: where it is, the venue on
// the rating site, and — for a Representative reading their own page — the way
// back into the host view. One muted span, so the links are the same size as
// the town beside them and the dots between are the kit's own separator.
func venueHeadLine(d VenueDetail) *ui.Element {
	var parts []ui.Item
	add := func(item ui.Item) {
		if len(parts) > 0 {
			parts = append(parts, ui.Text(" · "))
		}
		parts = append(parts, item)
	}
	if d.City != "" {
		add(ui.Text(d.City))
	}
	if d.RatingVenueID > 0 {
		add(ratingVenueLink(d.RatingVenueID))
	}
	if d.IsHost {
		add(ui.Link(ui.Href("/host/venue/"+d.Ref), ui.Text(strs.Venues.Public.HostLink())))
	}
	if len(parts) == 0 {
		return nil
	}
	// Inline, because text mixed with links has no unambiguous reading; Row,
	// because a bare inline is not a block and needs somewhere to sit.
	return ui.Row(ui.AlignCenter, ui.Wrap(), ui.Muted(ui.Inline(parts...)))
}

func VenueDoc(d VenueDetail) *ui.Doc {
	page := []ui.Item{ui.Title(d.Title), ui.PagePublic}
	page = append(page, jumpHostNav("/host/venue/"+d.Ref, dopestrings.Default.Host.Pages.JumpHostLabel(), dopestrings.Default.Host.Pages.JumpHostTitleFest())...)
	page = append(page, ui.Publictopbar(ui.Crumbs(
		pages.HomeCrumb(), ui.Crumb(ui.Href("/venues"), ui.Text(strs.Venues.Public.IndexTitle())), pages.Leaf(d.Title))))
	if line := venueHeadLine(d); line != nil {
		page = append(page, line)
	}
	if d.Description != "" {
		page = append(page, ui.Richtext(ui.Raw(string(d.Description))))
	}
	if len(d.Upcoming) == 0 && len(d.Past) == 0 {
		page = append(page, ui.Empty(ui.Text(strs.Venues.Public.GamesEmpty())))
	}
	if len(d.Upcoming) > 0 {
		page = append(page, slotTable(strs.Venues.Public.UpcomingGames(), d.Upcoming))
	}
	if len(d.Past) > 0 {
		page = append(page, slotTable(strs.Venues.Public.PastGames(), d.Past))
	}
	if len(d.Mine) > 0 {
		page = append(page, mineHereSection(d.Mine))
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

// rosterFlagsTable shows a roster as it was saved, flags and all: they are
// derived on save, so the form never offers them.
func rosterFlagsTable(players []venues.RosterPlayer, flags []string) *ui.Element {
	table := []ui.Item{ui.Scroll(), ui.Trow(ui.Hcell(ui.Text(strs.Venues.Reg.ColPlayer())), ui.Hcell(ui.Text(strs.Venues.Reg.ColId())), ui.Hcell(ui.Text(strs.Venues.Reg.ColFlag())))}
	for i, p := range players {
		flag := ""
		if i < len(flags) {
			flag = flags[i]
		}
		id := ""
		if p.PlayerID > 0 {
			id = strconv.FormatInt(p.PlayerID, 10)
		}
		table = append(table, ui.Trow(ui.Cell(ui.Text(p.FullName())), ui.Cell(ui.Text(id)), ui.Cell(ui.Text(flag))))
	}
	return ui.Table(table...)
}

func rosterEditor(players []venues.RosterPlayer, at string) *ui.Element {
	return ui.Col(ui.SpaceSM, ui.Data("roster-editor", at),
		ui.Hiddenfield(ui.Data("roster-json", ""), ui.Name("roster_json"), ui.Value(venues.MarshalRoster(players))),
	)
}

// applicationForm is the application as its submitter edits it, roster and all.
// Naming six players used to wait for the seat, on the grounds that it is work;
// it is not, once the base roster and the last one used are each one button
// away.
func applicationForm(action string, app *ApplicationView, submit, at string) *ui.Element {
	teamName, alias, ratingID := "", "", ""
	var players []venues.RosterPlayer
	if app != nil {
		// TeamName is the alias when there is one, and the box above the alias
		// is for the team itself, so it shows what the rating id names instead.
		teamName = app.TeamName
		alias = app.Alias
		if alias != "" {
			teamName = app.RealTeamName
		}
		if app.RatingTeamID > 0 {
			ratingID = strconv.FormatInt(app.RatingTeamID, 10)
		}
		players = app.Roster
	}
	form := []ui.Item{ui.DirCol, ui.Method("post"), ui.Action(action), ui.Autocomplete("off"),
		teamKindField(app),
		// One box either way. For an existing team it is a suggest over buff by
		// name or by id and the id it settles on goes in the hidden field; for a
		// new one it is just the name.
		ui.Field(ui.Label(strs.Venues.Reg.TeamNameLabel()),
			ui.Textfield(ui.Name("team_name"), ui.Value(teamName), ui.Required(),
				ui.Placeholder(strs.Venues.Reg.TeamSearchPlaceholder()),
				ui.Data("team-field", ""), ui.Autocomplete("off"))),
		ui.Hiddenfield(ui.Data("team-id", ""), ui.Name("rating_team_id"), ui.Value(ratingID)),
		aliasField(teamKind(app), alias),
	}
	form = append(form, ui.Field(ui.Label(strs.Venues.Reg.RosterLabel()), rosterEditor(players, at)))
	form = append(form, ui.Row(ui.Button(ui.Submit(), ui.Text(submit))))
	return ui.Form(form...)
}

// teamKind is which of the two an application is for: a team with no rating id
// behind it was typed by hand, and a form nobody has filed yet starts on the
// one that most applications are.
func teamKind(app *ApplicationView) string {
	if app != nil && app.RatingTeamID == 0 && app.TeamName != "" {
		return "new"
	}
	return "existing"
}

// aliasField is the name a team is announced under for this one game, when it
// is not the name it plays under. It belongs to an existing team only — a new
// team's one-off name is simply its name — and sits behind a tick, because an
// empty box beside the team's own name only invites the question of which of
// the two counts.
func aliasField(kind, alias string) *ui.Element {
	toggle := []ui.Item{ui.Name("team_alias_on"), ui.Value("1"), ui.Text(strs.Venues.Reg.TeamAliasToggle())}
	field := []ui.Item{ui.Data("when", "team_alias_on")}
	outer := []ui.Item{ui.SpaceSM, ui.Data("when", "team_kind=existing")}
	if alias != "" {
		toggle = append(toggle, ui.Checked())
	} else {
		field = append(field, ui.Hidden())
	}
	if kind != "existing" {
		outer = append(outer, ui.Hidden())
	}
	return ui.Col(append(outer, ui.Checkbox(toggle...),
		ui.Col(append(field, ui.Field(ui.Label(strs.Venues.Reg.TeamAliasLabel()),
			ui.Textfield(ui.Name("team_alias"), ui.Value(alias),
				ui.Placeholder(strs.Venues.Reg.TeamAliasPlaceholder()), ui.Autocomplete("off"))))...))...)
}

// teamKindField is the choice the box after it obeys. An application already
// filed picks whichever it was: a team with a rating id is an existing one.
func teamKindField(app *ApplicationView) *ui.Element {
	existing := []ui.Item{ui.Name("team_kind"), ui.Value("existing"), ui.Text(strs.Venues.Reg.TeamKindExisting())}
	fresh := []ui.Item{ui.Name("team_kind"), ui.Value("new"), ui.Text(strs.Venues.Reg.TeamKindNew())}
	if teamKind(app) == "new" {
		fresh = append(fresh, ui.Checked())
	} else {
		existing = append(existing, ui.Checked())
	}
	return ui.Pickgroup(ui.Label(strs.Venues.Reg.TeamKindLabel()),
		ui.Row(ui.SpaceSM, ui.AlignCenter, ui.Wrap(), ui.Radio(existing...), ui.Radio(fresh...)))
}

var statusLabels = map[string]string{
	venues.StatusPending:  strs.Venues.Reg.StatusPending(),
	venues.StatusAccepted: strs.Venues.Reg.StatusAccepted(),
	venues.StatusDeclined: strs.Venues.Reg.StatusDeclined(),
}

func StatusLabel(status string) string {
	if label, ok := statusLabels[status]; ok {
		return label
	}
	return status
}

func RegDoc(p RegPage) *ui.Doc {
	page := []ui.Item{ui.Title(strs.Venues.Reg.Title(p.VenueTitle)), ui.PagePublic,
		ui.Classicscripts("dist/pageforms.js dist/roster-editor.js")}
	page = append(page, ui.Publictopbar(ui.Crumbs(
		pages.HomeCrumb(), ui.Crumb(ui.Href("/venues"), ui.Text(strs.Venues.Public.IndexTitle())),
		ui.Crumb(ui.Href("/venue/"+p.VenueRef), ui.Text(p.VenueTitle)), pages.Leaf(strs.Venues.Reg.Crumb()))))

	page = append(page, ui.Section(
		ui.Subhead(ui.Text(p.VenueTitle)),
		ui.Note(ui.Text(joinDots(venues.HumanDate(p.Date), p.City, p.Tournament))),
	))

	if p.Error != "" {
		page = append(page, ui.Empty(ui.Text(p.Error)))
	}
	if p.Notice != "" {
		page = append(page, ui.Hint(ui.Text(p.Notice)))
	}

	switch {
	case p.State == venues.RegScheduled:
		page = append(page, ui.Empty(ui.Text(strs.Venues.Reg.Scheduled(p.OpensAt))))
	// A closed registration is what a Slot starts with, so the page says so
	// before it asks anyone to log in for a application they cannot file.
	case p.State == venues.RegClosed && !p.LoggedIn:
		page = append(page, ui.Empty(ui.Text(strs.Venues.Reg.Closed())))
	case !p.LoggedIn:
		page = append(page, ui.Section(
			ui.Hint(ui.Text(strs.Venues.Reg.LoginHint())),
			ui.Row(ui.Button(ui.Primary, ui.Href(p.LoginHref), ui.Text(strs.Venues.Reg.LoginBtn()))),
		))
	default:
		page = append(page, applicationSection(p))
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func applicationSection(p RegPage) *ui.Element {
	action := "/reg/" + p.Token
	sect := []ui.Item{ui.Subhead(ui.Text(strs.Venues.Reg.ApplicationSubhead()))}
	if p.Application != nil {
		number := ""
		if p.Application.Number > 0 {
			number = strs.Venues.Reg.TeamNumber(strconv.FormatInt(p.Application.Number, 10))
		}
		sect = append(sect, ui.Note(ui.Text(joinDots(strs.Venues.Reg.StatusLine(p.Application.StatusLabel), number))))
		if p.Application.Status == venues.StatusAccepted && p.GameHref != "" {
			sect = append(sect, ui.Row(ui.Button(ui.Primary, ui.Href(p.GameHref), ui.Text(strs.Venues.Reg.TableBtn()))))
		}
	}
	if p.State == venues.RegClosed && p.Application == nil {
		sect = append(sect, ui.Empty(ui.Text(strs.Venues.Reg.Closed())))
		return ui.Section(sect...)
	}
	submit := strs.Venues.Reg.SubmitNew()
	if p.Application != nil {
		submit = strs.Venues.Reg.SubmitEdit()
	}
	sect = append(sect, applicationForm(action, p.Application, submit, p.Date))
	return ui.Section(sect...)
}
