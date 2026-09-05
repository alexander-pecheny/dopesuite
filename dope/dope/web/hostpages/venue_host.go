package hostpages

import (
	"strconv"
	"strings"
	"time"

	"dope/dope/domain/venues"
	"dope/dope/storage/festaccess"
	"dope/dope/web/pages"
	ui "dope/dope/web/ui"

	dopestrings "dope/i18nstrings"
)

// strs is dope's Catalog; the venue pages read their words from strs.Venues
// (i18nstrings/ru/venues.toml) — the venue pages, the game page under one, and
// its registration, poll and queue.
var strs = dopestrings.Default

type VenueDashSlot struct {
	ID         int64
	Date       string
	Tournament string
	Accepted   int
	Pending    int
	Href       string
}

type venueDashData struct {
	Venue     venues.Venue
	Slots     []VenueDashSlot
	Access    []festaccess.HostAccessMember
	CanManage bool
	CanDelete bool
	Tz        string
	Error     string
	Notice    string
}

func venueCrumbs(v venues.Venue) []ui.Item {
	return append(pages.HostCrumbs(), ui.Crumb(ui.Href(VenueBase(v)), ui.Text(v.Title)))
}

func venueDashDoc(data venueDashData) *ui.Doc {
	v := data.Venue
	page := []ui.Item{ui.Title(strs.Venues.Host.PageTitle(v.Title)), ui.PagePublic,
		ui.Classicscripts("dist/pageforms.js dist/roster-editor.js")}
	if v.IsPublic {
		page = append(page,
			ui.Data("jump-label", strs.Venues.Host.JumpLabel()),
			ui.Data("jump-href", "/venue/"+v.Ref()),
			ui.Data("jump-title", strs.Venues.Host.JumpTitle()),
			ui.Data("jump-icon", "eye"),
		)
	}
	page = append(page, ui.Publictopbar(pages.Trail(pages.HostCrumbs(), v.Title)))
	if data.Error != "" {
		page = append(page, ui.Empty(ui.Text(data.Error)))
	}
	if data.Notice != "" {
		page = append(page, ui.Hint(ui.Text(data.Notice)))
	}
	if data.CanManage {
		page = append(page, venueSettingsForm(v))
	}
	page = append(page, venueSlotsSection(data))
	if data.CanManage {
		page = append(page, hostDashAccessSection(hostFestDashData{Access: data.Access}, VenueBase(v)))
	}
	if data.CanDelete {
		page = append(page, ui.Section(
			ui.Subhead(ui.Text(strs.Venues.Host.DeleteSubhead())),
			ui.Form(ui.DirCol, ui.Method("post"), ui.Action(VenueBase(v)+"/delete"), ui.Autocomplete("off"),
				ui.Data("confirm", strs.Venues.Host.DeleteVenueConfirm()),
				ui.Row(ui.Button(ui.Danger, ui.Submit(), ui.Text(strs.Venues.Host.DeleteVenueSubmit()))),
			),
		))
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func venueSettingsForm(v venues.Venue) *ui.Element {
	ratingID := ""
	if v.RatingVenueID > 0 {
		ratingID = strconv.FormatInt(v.RatingVenueID, 10)
	}
	pub := []ui.Item{ui.Name("is_public"), ui.Value("1"), ui.Text(strs.Venues.Host.PublicLabel())}
	if v.IsPublic {
		pub = append(pub, ui.Checked())
	}
	return ui.Form(ui.DirCol, ui.Method("post"), ui.Action(VenueBase(v)), ui.Autocomplete("off"),
		ui.Field(ui.Label(strs.Venues.Host.TitleLabel()), ui.Textfield(ui.Name("title"), ui.Value(v.Title), ui.Required(), ui.Data("venue-name", ""))),
		ui.Field(ui.Label(strs.Venues.Host.CityLabel()), ui.Textfield(ui.Name("city"), ui.Value(v.City), ui.Data("venue-city", ""))),
		ui.Field(ui.Label(strs.Venues.Host.DescriptionLabel()), ui.Editor(ui.Name("description"), ui.Rows("6"), ui.Text(v.Description))),
		slugField(strs.Venues.Host.SlugLabel(), v.Slug),
		ratingVenueField(ratingID, false),
		ui.Checkbox(pub...),
		ui.Row(ui.Button(ui.Submit(), ui.Text(strs.Venues.Host.SaveSubmit()))),
	)
}

func venueSlotsSection(data venueDashData) *ui.Element {
	ref := VenueBase(data.Venue)
	sect := []ui.Item{ui.Subhead(ui.Text(strs.Venues.Host.GamesSubhead()))}
	if len(data.Slots) == 0 {
		sect = append(sect, ui.Empty(ui.Text(strs.Venues.Host.GamesEmpty())))
	} else {
		rows := make([]ui.Item, 0, len(data.Slots))
		for _, s := range data.Slots {
			title := s.Date
			if title == "" {
				title = strs.Venues.Host.GameUndated()
			}
			row := []ui.Item{ui.Href(s.Href), ui.Listtitle(ui.Text(title))}
			sub := s.Tournament
			if sub == "" {
				sub = strs.Venues.Host.GameNoTournament()
			}
			sub += strs.Venues.Host.GameAccepted(strconv.Itoa(s.Accepted))
			if s.Pending > 0 {
				sub += strs.Venues.Host.GamePending(strconv.Itoa(s.Pending))
			}
			row = append(row, ui.Muted(ui.Text(sub)))
			rows = append(rows, ui.Listrow(row...))
		}
		sect = append(sect, ui.List(rows...))
	}
	if data.CanManage {
		sect = append(sect, ui.Details(
			ui.Summary(ui.Btn(), ui.Text(strs.Venues.Host.NewGameSummary())),
			ui.Form(ui.DirCol, ui.Method("post"), ui.Action(ref+"/game/new"), ui.Autocomplete("off"),
				ui.Field(ui.Label(strs.Venues.Game.DatetimeLabel()),
					ui.Datetimefield(ui.Name("starts_at"), ui.Placeholder("2026-09-04 19:00"), ui.Required(),
						ui.Data("datetime-tz", data.Tz))),
				ui.Row(ui.Button(ui.Submit(), ui.Text(strs.Venues.Host.NewGameSubmit()))),
			),
		))
	}
	return ui.Section(sect...)
}

type SlotApplicationRow struct {
	App         venues.Application
	Flags       []string
	FlagSummary string
	Submitter   string
	SubmitterTg string
	Versions    []venues.Version
}

type slotPageData struct {
	Venue        venues.Venue
	Slot         venues.Slot
	Tournament   string
	GameStatus   string
	GameHref     string
	Applications []SlotApplicationRow
	Voting       VotingView
	Contested    []ContestedRow
	RegURL       string
	RegState     venues.RegState
	CanManage    bool
	Tz           string
	Error        string
	Notice       string
}

// slotTitle is short, for a crumb and a tab; slotHuman is the same moment
// spelled out, for the one line on the page that says which evening this is.
func slotTitle(slot venues.Slot) string {
	if slot.StartsAt == "" {
		return strs.Venues.Game.UndatedTitle()
	}
	return slot.StartsAt
}

func slotHuman(slot venues.Slot) string {
	if slot.StartsAt == "" {
		return strs.Venues.Game.UndatedTitle()
	}
	return venues.HumanDate(slot.StartsAt)
}

// VenueBase is the Representative's tree for one Venue; a Venue is a Fest
// of its own kind and has host pages of its own.
func VenueBase(v venues.Venue) string { return "/host/venue/" + v.Ref() }

func slotBase(v venues.Venue, slot venues.Slot) string {
	return VenueBase(v) + "/game/" + slot.GameRef()
}

func slotPageDoc(data slotPageData) *ui.Doc {
	page := []ui.Item{ui.Title(slotTitle(data.Slot) + " · " + data.Venue.Title), ui.PagePublic,
		ui.Classicscripts("dist/pageforms.js dist/roster-editor.js")}
	page = append(page, ui.Publictopbar(ui.Crumbs(
		append(venueCrumbs(data.Venue), pages.Leaf(slotTitle(data.Slot)))...)))
	if data.Error != "" {
		page = append(page, ui.Empty(ui.Text(data.Error)))
	}
	if data.Notice != "" {
		page = append(page, ui.Hint(ui.Text(data.Notice)))
	}
	page = append(page, slotHeaderSection(data), slotRegSection(data), slotApplicationsSection(data),
		slotVotingSection(data), slotContestedSection(data), slotDownloadsSection(data))
	if data.CanManage {
		page = append(page, slotDeleteSection(data))
	}
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func slotHeaderSection(data slotPageData) *ui.Element {
	base := slotBase(data.Venue, data.Slot)
	tournamentID := ""
	if data.Slot.RatingTournamentID > 0 {
		tournamentID = strconv.FormatInt(data.Slot.RatingTournamentID, 10)
	}
	sect := []ui.Item{
		ui.Subhead(ui.Text(strs.Venues.Game.Subhead())),
		ui.Note(ui.Text(joinDots(slotHuman(data.Slot), data.Tournament, data.GameStatus))),
		ui.Row(ui.Button(ui.Ghost, ui.Small(), ui.Href(data.GameHref), ui.Text(strs.Venues.Game.TableBtn()))),
	}
	if !data.CanManage {
		return ui.Section(sect...)
	}
	sect = append(sect,
		ui.Form(ui.DirCol, ui.Method("post"), ui.Action(base), ui.Autocomplete("off"),
			ui.Field(ui.Label(strs.Venues.Game.DatetimeLabel()), ui.Datetimefield(ui.Name("starts_at"), ui.Value(data.Slot.StartsAt),
				ui.Data("datetime-tz", data.Tz))),
			ui.Field(ui.Label(strs.Venues.Game.TournamentLabel()),
				ui.Textfield(ui.Name("rating_tournament_id"), ui.Value(tournamentID),
					ui.Placeholder(strs.Venues.Game.TournamentPlaceholder()),
					ui.Data("buff-tournament", data.Slot.StartsAt), ui.Autocomplete("off"))),
			ui.Row(ui.Button(ui.Submit(), ui.Text(strs.Venues.Host.SaveSubmit()))),
		),
		ui.Row(ui.Button(ui.Ghost, ui.Data("dialog-open", "cloneSlot"), ui.Text(strs.Venues.Game.CloneBtn()))),
		ui.Dialog(ui.ID("cloneSlot"),
			ui.Form(ui.DirCol, ui.Method("post"), ui.Action(base+"/clone"), ui.Autocomplete("off"),
				ui.Subhead(ui.Text(strs.Venues.Game.CloneTitle())),
				ui.Note(ui.Text(strs.Venues.Game.CloneNote())),
				ui.Field(ui.Label(strs.Venues.Game.CloneDatetimeLabel()),
					ui.Datetimefield(ui.Name("starts_at"), ui.Value(venues.Shift(data.Slot.StartsAt, 7*24*time.Hour)),
						ui.Required(), ui.Data("datetime-tz", data.Tz))),
				ui.Row(
					ui.Button(ui.Submit(), ui.Text(strs.Venues.Game.CloneBtn())),
					ui.Button(ui.Data("dialog-close", ""), ui.Text(strs.Venues.Game.Cancel())),
				),
			),
		),
	)
	return ui.Section(sect...)
}

func slotDeleteSection(data slotPageData) *ui.Element {
	return ui.Section(
		ui.Subhead(ui.Text(strs.Venues.Host.DeleteSubhead())),
		ui.Form(ui.DirCol, ui.Method("post"), ui.Action(slotBase(data.Venue, data.Slot)+"/delete"), ui.Autocomplete("off"),
			ui.Data("confirm", strs.Venues.Game.DeleteConfirm()),
			ui.Row(ui.Button(ui.Danger, ui.Submit(), ui.Text(strs.Venues.Game.DeleteSubmit()))),
		),
	)
}

// slotRegSection is the whole registration in one place. The link exists from
// the moment the Slot does and works from it; what the dialog decides is the
// window it runs on and whether the Venue's public page carries it.
func slotRegSection(data slotPageData) *ui.Element {
	base := slotBase(data.Venue, data.Slot)
	sect := []ui.Item{
		ui.Subhead(ui.Text(strs.Venues.Game.RegSubhead())),
		ui.Note(ui.Text(regStateLine(data))),
	}
	sect = append(sect,
		ui.Field(ui.Label(strs.Venues.Game.LinkLabel()),
			ui.Row(ui.SpaceSM, ui.AlignCenter,
				ui.Textfield(ui.Grow(), ui.ID("regLink"), ui.Value(data.RegURL), ui.Readonly(), ui.Data("select-all", "")),
				ui.Button(ui.Ghost, ui.IconCopy, ui.Data("copy-target", "regLink"),
					ui.Title(strs.Venues.Game.LinkCopy()), ui.Aria("label", strs.Venues.Game.LinkCopy())),
			)),
	)
	if !data.CanManage {
		return ui.Section(sect...)
	}
	sect = append(sect,
		ui.Row(ui.SpaceSM, ui.AlignCenter, ui.Wrap(),
			ui.Button(ui.Primary, ui.Data("dialog-open", "slotReg"), ui.Text(strs.Venues.Game.RegDialogTitle())),
			shutForm(data, base),
			ui.Form(ui.Method("post"), ui.Action(base+"/token"),
				ui.Data("confirm", strs.Venues.Game.LinkRotateConfirm()),
				ui.Button(ui.Ghost, ui.Submit(), ui.Text(strs.Venues.Game.LinkRotate())),
			),
		),
		slotRegDialog(data, base),
	)
	return ui.Section(sect...)
}

// shutForm is the one button that closes applications now and the one that
// opens them again: which of the two it is is which way the registration
// currently stands, so the button is never a no-op.
func shutForm(data slotPageData, base string) *ui.Element {
	form := []ui.Item{ui.Method("post"), ui.Action(base + "/shut")}
	if data.Slot.RegShut {
		return ui.Form(append(form,
			ui.Hiddenfield(ui.Name("shut"), ui.Value("0")),
			ui.Button(ui.Ghost, ui.Submit(), ui.Text(strs.Venues.Game.RegOpenBtn())))...)
	}
	return ui.Form(append(form,
		ui.Data("confirm", strs.Venues.Game.RegShutConfirm()),
		ui.Hiddenfield(ui.Name("shut"), ui.Value("1")),
		ui.Button(ui.Ghost, ui.Submit(), ui.Text(strs.Venues.Game.RegShutBtn())))...)
}

// regStateLine is where the registration stands: whether it takes applications
// now, and the ends of the window that decide it.
func regStateLine(data slotPageData) string {
	state := strs.Venues.Game.RegStateOpen()
	switch {
	case data.Slot.RegShut:
		state = strs.Venues.Game.RegStateShut()
	case data.RegState == venues.RegScheduled:
		state = strs.Venues.Game.RegStateScheduled(data.Slot.RegOpensAt)
	case data.RegState == venues.RegClosed:
		state = strs.Venues.Game.RegStateClosed()
	}
	until := ""
	if data.Slot.RegClosesAt != "" && data.RegState != venues.RegClosed {
		until = strs.Venues.Game.RegUntil(data.Slot.RegClosesAt)
	}
	public := ""
	if data.Slot.LinkVisible {
		public = strs.Venues.Game.RegLinkPublic()
	}
	return joinDots(state, until, public)
}

// whenField is one end of the registration's window: a Slot either has no such
// end at all or has a moment for it, so the two are radios and the calendar
// belongs to the second of them.
func whenField(label, group, never, dated, value, tz string) *ui.Element {
	atItems := []ui.Item{ui.Name(group), ui.Value("at"), ui.Text(dated)}
	neverItems := []ui.Item{ui.Name(group), ui.Value("never"), ui.Text(never)}
	pick := []ui.Item{ui.Data("when", group+"=at")}
	if value == "" {
		neverItems = append(neverItems, ui.Checked())
		pick = append(pick, ui.Hidden())
	} else {
		atItems = append(atItems, ui.Checked())
	}
	return ui.Pickgroup(ui.Label(label),
		ui.Row(ui.SpaceSM, ui.AlignCenter, ui.Wrap(),
			ui.Radio(neverItems...), ui.Radio(atItems...),
			ui.Col(append(pick, ui.Datetimefield(ui.Name(group+"_at"), ui.Value(value),
				ui.Data("datetime-tz", tz)))...),
		))
}

func slotRegDialog(data slotPageData, base string) *ui.Element {
	visible := []ui.Item{ui.Name("link_visible"), ui.Value("1"), ui.Text(strs.Venues.Game.LinkPublicLabel())}
	if data.Slot.LinkVisible {
		visible = append(visible, ui.Checked())
	}
	return ui.Dialog(ui.ID("slotReg"),
		ui.Form(ui.DirCol, ui.Method("post"), ui.Action(base+"/reg"), ui.Autocomplete("off"),
			ui.Subhead(ui.Text(strs.Venues.Game.RegDialogTitle())),
			whenField(strs.Venues.Game.RegOpensLabel(), "reg_opens", strs.Venues.Game.RegOpensNow(),
				strs.Venues.Game.RegOpensAtLabel(), data.Slot.RegOpensAt, data.Tz),
			whenField(strs.Venues.Game.RegClosesLabel(), "reg_closes", strs.Venues.Game.RegClosesNever(),
				strs.Venues.Game.RegClosesAtLabel(), data.Slot.RegClosesAt, data.Tz),
			ui.Checkbox(visible...),
			ui.Row(
				ui.Button(ui.Submit(), ui.Text(strs.Venues.Host.SaveSubmit())),
				ui.Button(ui.Ghost, ui.Data("dialog-close", ""), ui.Text(strs.Venues.Game.Cancel())),
			),
		),
	)
}

func applicationActions(base string, row SlotApplicationRow) *ui.Element {
	actions := []ui.Item{ui.Method("post"), ui.Action(base + "/application/" + strconv.FormatInt(row.App.ID, 10) + "/status")}
	for _, a := range []struct{ status, label string }{
		{venues.StatusAccepted, strs.Venues.Game.StatusAccept()},
		{venues.StatusDeclined, strs.Venues.Game.StatusDecline()},
		{venues.StatusPending, strs.Venues.Game.StatusPending()},
	} {
		if a.status == row.App.Status {
			continue
		}
		actions = append(actions, ui.Button(ui.Ghost, ui.Small(), ui.Submit(),
			ui.Name("status"), ui.Value(a.status), ui.Text(a.label)))
	}
	return ui.Form(actions...)
}

func applicationVersions(base string, row SlotApplicationRow) *ui.Element {
	appID := strconv.FormatInt(row.App.ID, 10)
	items := []ui.Item{ui.Summary(ui.Text(strs.Venues.Game.VersionsSummary()))}
	table := []ui.Item{ui.Trow(ui.Hcell(ui.Text(strs.Venues.Game.ColNumber())), ui.Hcell(ui.Text(strs.Venues.Public.ColWhen())), ui.Hcell(ui.Text(strs.Venues.Game.ColWho())),
		ui.Hcell(ui.Text(strs.Venues.Game.ColTeam())), ui.Hcell(ui.Text(strs.Venues.Game.ColRoster())), ui.Hcell(ui.Text("")))}
	for _, v := range row.Versions {
		names := make([]string, 0, len(v.Roster))
		for _, p := range v.Roster {
			names = append(names, p.FullName())
		}
		table = append(table, ui.Trow(
			ui.Cell(ui.Text(strconv.FormatInt(v.Seq, 10))),
			ui.Cell(ui.Text(venues.HumanTime(v.CreatedAt))),
			ui.Cell(ui.Text(v.Author)),
			ui.Cell(ui.Text(v.TeamName)),
			ui.Cell(ui.Text(strings.Join(names, ", "))),
			ui.Cell(ui.Form(ui.Method("post"), ui.Action(base+"/application/"+appID+"/revert"),
				ui.Button(ui.Ghost, ui.Small(), ui.Submit(), ui.Name("seq"), ui.Value(strconv.FormatInt(v.Seq, 10)),
					ui.Text(strs.Venues.Game.VersionRestore())))),
		))
	}
	return ui.Details(append(items, ui.Table(append([]ui.Item{ui.Scroll()}, table...)...))...)
}

func slotApplicationsSection(data slotPageData) *ui.Element {
	base := slotBase(data.Venue, data.Slot)
	sect := []ui.Item{ui.Subhead(ui.Text(strs.Venues.Game.ApplicationsSubhead()))}
	if len(data.Applications) == 0 {
		return ui.Section(append(sect, ui.Empty(ui.Text(strs.Venues.Game.ApplicationsEmpty())))...)
	}
	table := []ui.Item{ui.Scroll(), ui.Trow(
		ui.Hcell(ui.Text(strs.Venues.Game.ColNumber())), ui.Hcell(ui.Text(strs.Venues.Game.ColTeam())), ui.Hcell(ui.Text(strs.Venues.Public.ColRating())),
		ui.Hcell(ui.Text(strs.Venues.Game.ColSubmitter())), ui.Hcell(ui.Text(strs.Venues.Game.ColFiled())), ui.Hcell(ui.Text(strs.Venues.Game.ColEdited())),
		ui.Hcell(ui.Text(strs.Venues.Game.ColRoster())), ui.Hcell(ui.Text(strs.Venues.Game.ColFlags())), ui.Hcell(ui.Text(strs.Venues.Game.ColStatus())),
	)}
	for _, row := range data.Applications {
		number := ""
		if row.App.Number > 0 {
			number = strconv.FormatInt(row.App.Number, 10)
		}
		rating := ui.Cell(ui.Text(""))
		if row.App.RatingTeamID > 0 {
			id := strconv.FormatInt(row.App.RatingTeamID, 10)
			rating = ui.Cell(ui.Link(ui.Href("https://rating.chgk.info/teams/"+id), ui.Newtab(), ui.Text(id)))
		}
		submitter := ui.Cell(ui.Text(row.Submitter))
		if row.SubmitterTg != "" {
			submitter = ui.Cell(ui.Link(ui.Href(row.SubmitterTg), ui.Newtab(), ui.Text(row.Submitter)))
		}
		table = append(table, ui.Trow(
			ui.Cell(ui.Text(number)),
			ui.Cell(ui.Text(row.App.TeamName)),
			rating,
			submitter,
			ui.Cell(ui.Text(venues.HumanTime(row.App.CreatedAt))),
			ui.Cell(ui.Text(venues.HumanTime(row.App.UpdatedAt))),
			ui.Cell(ui.Text(strconv.Itoa(len(row.App.Roster)))),
			ui.Cell(ui.Text(row.FlagSummary)),
			ui.Cell(ui.Text(StatusLabel(row.App.Status))),
		))
	}
	sect = append(sect, ui.Table(table...))
	if data.CanManage {
		for _, row := range data.Applications {
			sect = append(sect, ui.Details(
				ui.Summary(ui.Text(row.App.TeamName+" · "+StatusLabel(row.App.Status))),
				rosterFlagsTable(row.App.Roster, row.Flags),
				applicationActions(base, row),
				applicationVersions(base, row),
				applicationForm(base+"/application/"+strconv.FormatInt(row.App.ID, 10)+"/edit",
					&ApplicationView{TeamName: row.App.TeamName, RatingTeamID: row.App.RatingTeamID, Roster: row.App.Roster},
					strs.Venues.Game.VersionSave(), data.Slot.StartsAt),
			))
		}
	}
	return ui.Section(sect...)
}

func slotDownloadsSection(data slotPageData) *ui.Element {
	base := slotBase(data.Venue, data.Slot)
	return ui.Section(
		ui.Subhead(ui.Text(strs.Venues.Game.DownloadsSubhead())),
		ui.Row(ui.SpaceSM, ui.Wrap(),
			ui.Button(ui.Ghost, ui.Href(base+"/export/tours.xlsx"), ui.Download(), ui.Text(strs.Venues.Game.DownloadTours())),
			ui.Button(ui.Ghost, ui.Href(base+"/export/players.xlsx"), ui.Download(), ui.Text(strs.Venues.Game.DownloadPlayers())),
		),
	)
}

// SlugPattern and SlugTitle are what util.ValidateSlug takes, said to the
// browser so a bad slug is refused before the form is posted.
// The hyphen is escaped: a bare one inside a class is a syntax error under the
// v flag a browser compiles `pattern` with, and a pattern it cannot compile is
// one it silently ignores.
const SlugPattern = `[a-z0-9\-]*[a-z\-][a-z0-9\-]*`

// SlugTitle is what the field says when the pattern refuses.
var SlugTitle = strs.Venues.Host.SlugHint()

func slugField(label, value string) *ui.Element {
	return ui.Field(ui.Label(label),
		ui.Textfield(ui.Name("slug"), ui.Value(value), ui.Pattern(SlugPattern),
			ui.Title(SlugTitle), ui.Placeholder("my-venue")),
		ui.Hint(ui.Text(SlugTitle)))
}

func venueCreateForm(data hostLandingData) *ui.Element {
	items := []ui.Item{ui.Summary(ui.Btn(), ui.Text(strs.Venues.Host.CreateVenueSummary()))}
	if data.Open == "venue" {
		items = append([]ui.Item{ui.Open()}, items...)
		if data.Error != "" {
			items = append(items, ui.Hint(ui.HintDanger, ui.Text(data.Error)))
		}
	}
	return ui.Details(append(items,
		ui.Form(ui.DirCol, ui.Method("post"), ui.Action("/host/venue"), ui.Autocomplete("off"),
			ratingVenueField(data.kept("rating_venue_id"), true),
			slugField(strs.Venues.Host.SlugLabel(), data.kept("slug")),
			ui.Field(ui.Label(strs.Venues.Host.DescriptionLabel()), ui.Editor(ui.Name("description"), ui.Rows("4"), ui.Text(data.kept("description")))),
			checkboxKept("is_public", strs.Venues.Host.PublicLabel(), data.checked("is_public")),
			ui.Row(ui.Button(ui.Submit(), ui.Text(strs.Venues.Host.CreateVenueSubmit()))),
		))...)
}

// ratingVenueField is the venue on rating.chgk.info a Venue stands for: a
// suggest over their catalogue by id, name or town. A Venue is one of
// theirs, so the create form asks for nothing else — the name and the town come
// with the pick.
func ratingVenueField(value string, required bool) *ui.Element {
	field := []ui.Item{ui.Name("rating_venue_id"), ui.Value(value),
		ui.Placeholder(strs.Venues.Host.RatingVenuePlaceholder()), ui.Data("rating-venue", "")}
	if required {
		field = append(field, ui.Required())
	}
	return ui.Field(ui.Label(strs.Venues.Host.RatingVenueLabel()), ui.Textfield(field...))
}
