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
	CanManage    bool
	Tz           string
	Error        string
	Notice       string
}

func slotTitle(slot venues.Slot) string {
	if slot.StartsAt == "" {
		return strs.Venues.Game.UndatedTitle()
	}
	return slot.StartsAt
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
	page = append(page, slotHeaderSection(data), slotLinksSection(data), slotApplicationsSection(data),
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
		ui.Note(ui.Text(joinDots(slotTitle(data.Slot), data.Tournament, data.GameStatus))),
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
					ui.Button(ui.Data("dialog-close", ""), ui.Text(strs.Venues.Game.CloneCancel())),
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

func slotLinksSection(data slotPageData) *ui.Element {
	base := slotBase(data.Venue, data.Slot)
	sect := []ui.Item{ui.Subhead(ui.Text(strs.Venues.Game.RegSubhead()))}
	if data.CanManage {
		sect = append(sect, slotRegForm(data, base))
	}
	// The link is handed over only when the game says so. This page is the one
	// place it is handed over at all, so the tickbox is the whole switch.
	if !data.Slot.LinkVisible {
		return ui.Section(append(sect, ui.Empty(ui.Text(strs.Venues.Game.LinkHidden())))...)
	}
	sect = append(sect,
		ui.Field(ui.Label(strs.Venues.Game.LinkLabel()),
			ui.Row(ui.SpaceSM, ui.AlignCenter, ui.Wrap(),
				ui.Textfield(ui.ID("regLink"), ui.Value(data.RegURL), ui.Readonly(), ui.Data("select-all", "")),
				ui.Button(ui.Ghost, ui.Small(), ui.Data("copy-target", "regLink"), ui.Text(strs.Venues.Game.LinkCopy())),
			)),
	)
	if data.CanManage {
		sect = append(sect, ui.Form(ui.Method("post"), ui.Action(base+"/token"),
			ui.Data("confirm", strs.Venues.Game.LinkRotateConfirm()),
			ui.Button(ui.Ghost, ui.Small(), ui.Submit(), ui.Text(strs.Venues.Game.LinkRotate())),
		))
	}
	return ui.Section(sect...)
}

// slotRegForm opens and shuts the registration. Whether it is open is a state,
// not a setting: the button names the move, and the save beside it says nothing
// about the state and so leaves it where it was.
func slotRegForm(data slotPageData, base string) *ui.Element {
	visible := []ui.Item{ui.Name("link_visible"), ui.Value("1"), ui.Text(strs.Venues.Game.LinkVisibleLabel())}
	if data.Slot.LinkVisible {
		visible = append(visible, ui.Checked())
	}
	toggle, way, kind := strs.Venues.Game.RegCloseBtn(), "close", ui.Ghost
	if data.Slot.RegClosed {
		toggle, way, kind = strs.Venues.Game.RegOpenBtn(), "open", ui.Primary
	}
	return ui.Form(ui.DirCol, ui.Method("post"), ui.Action(base+"/reg"), ui.Autocomplete("off"),
		ui.Field(ui.Label(strs.Venues.Game.RegOpensLabel()),
			ui.Datetimefield(ui.Name("reg_opens_at"), ui.Value(data.Slot.RegOpensAt),
				ui.Placeholder(strs.Venues.Game.RegOpensPlaceholder()), ui.Data("datetime-tz", data.Tz))),
		ui.Checkbox(visible...),
		ui.Row(
			ui.Button(ui.Submit(), ui.Text(strs.Venues.Host.SaveSubmit())),
			ui.Button(kind, ui.Submit(), ui.Name("reg"), ui.Value(way), ui.Text(toggle)),
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
					strs.Venues.Game.VersionSave(), true),
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
