package hostpages

import (
	"context"
	"database/sql"
	"dope/dope/domain/core"
	"dope/dope/domain/imports"
	"dope/dope/domain/numbering"
	"dope/dope/domain/overrides"
	"dope/dope/domain/roster"
	"dope/dope/domain/view"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"dope/dope/domain/entrants"
	"dope/dope/web/route"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/idstr"
)

type hostFestTeam struct {
	ID       int64
	RatingID int64
	Name     string
	City     string
	Players  int
	// Flags is the team's Divisions as the editor types them: short names,
	// comma separated (ADR-0020).
	Flags string
	// Hand marks a team the host made; Edited, one the host changed by hand
	// (ADR-0024).
	Hand   bool
	Edited bool
}

type hostFestPlayer struct {
	RatingID int64
	Name     string
	Team     string
}

type hostFestRosterData struct {
	Fest            view.HostFest
	Teams           []hostFestTeam
	Players         []hostFestPlayer
	OverridePlayers []overrides.HostPlayerOverrideOption
	OverrideTeams   []overrides.HostTeamOverrideOption
	OverrideGames   []overrides.HostGameOverrideOption
	Overrides       []overrides.HostPlayerOverrideRow
	Error           string
	Notice          string
}

type hostFestImportData struct {
	Fest     view.HostFest
	RatingID int64
	Error    string
	Notice   string
	// Conflict is set when the import stopped to ask what to do with teams that
	// are leaving the roster while they still carry results.
	Conflict *imports.RosterConflict
	// Plan is the preview of an import (ADR-0024), and Choice the answers it
	// was previewed with, which the confirm form carries on.
	Plan   *imports.ImportPlan
	Choice imports.RosterChoice
	// UndoAt is when the roster was saved before the last import, "" when
	// there is no import to undo.
	UndoAt string
}

// hostTeamsDoc builds the fest's teams table (or an empty note). The Divisions
// column is editable: the whole table is one form, so an organizer types the
// Flags of a fest the rating site does not carry and saves them in one go.
func hostTeamsDoc(data hostFestRosterData) *dopeui.Doc {
	s := dopestrings.Default
	ref := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(s.Host.Roster.TeamsTitle(data.Fest.Title)), dopeui.PagePublic,
		dopeui.Classicscripts("dist/pageforms.js dist/fest-teams.js"),
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(ref, data.Fest.Title), s.Host.Roster.TeamsCrumb())),
	}
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}
	if data.Notice != "" {
		page = append(page, dopeui.Note(dopeui.Text(data.Notice)))
	}
	// The editor (fest-teams.ts) reads the fest's API off the add button, and
	// each row's team off its pencil (ADR-0024).
	page = append(page, dopeui.Row(dopeui.SpaceSM, dopeui.Wrap(),
		dopeui.Button(dopeui.Data("team-add", "/api/fest/"+ref), dopeui.Text(s.Host.Roster.AddTeamBtn())),
		dopeui.Button(dopeui.Href("/api/fest/"+ref+"/teams/export.xlsx"), dopeui.Download(), dopeui.Text(s.Host.Roster.ExportXlsxBtn())),
		dopeui.Button(dopeui.Data("team-xlsx", ""), dopeui.Text(s.Host.Roster.ImportXlsxBtn())),
	))
	if len(data.Teams) > 0 {
		rows := []dopeui.Item{dopeui.Trow(
			dopeui.Hcell(dopeui.Text("ID")), dopeui.Hcell(dopeui.Text(s.Host.Roster.TeamLabel())),
			dopeui.Hcell(dopeui.Text(s.Host.Roster.ColCity())), dopeui.Hcell(dopeui.Text(s.Host.Roster.ColPlayers())),
			dopeui.Hcell(dopeui.Text(s.Host.Roster.ColFlags())), dopeui.Hcell(),
		)}
		for _, t := range data.Teams {
			name := []dopeui.Item{dopeui.Text(t.Name)}
			switch {
			case t.Hand:
				name = []dopeui.Item{dopeui.Row(dopeui.SpaceSM, dopeui.Wrap(), dopeui.Paragraph(dopeui.Text(t.Name)), dopeui.Badge(dopeui.Text(s.Host.Roster.BadgeHand())))}
			case t.Edited:
				name = []dopeui.Item{dopeui.Row(dopeui.SpaceSM, dopeui.Wrap(), dopeui.Paragraph(dopeui.Text(t.Name)), dopeui.Badge(dopeui.Text(s.Host.Roster.BadgeEdited())))}
			}
			rows = append(rows, dopeui.Trow(
				dopeui.Cell(dopeui.Text(optionalID(t.RatingID))),
				dopeui.Cell(name...),
				dopeui.Cell(dopeui.Text(t.City)),
				dopeui.Cell(dopeui.Text(strconv.Itoa(t.Players))),
				dopeui.Cell(dopeui.Textfield(
					dopeui.Name(teamFlagsField(t.ID)), dopeui.Value(t.Flags),
					dopeui.Placeholder(s.Host.Roster.FlagsPlaceholder()), dopeui.Autocomplete("off"),
				)),
				dopeui.Cell(dopeui.Iconbtn(dopeui.IconPencil, dopeui.Label(s.Host.Roster.EditTeamLabel(t.Name)),
					dopeui.Data("team-edit", idstr.Format(t.ID)))),
			))
		}
		page = append(page, dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/teams"), dopeui.Autocomplete("off"),
			dopeui.Note(dopeui.Text(s.Host.Roster.TeamsEditHint())),
			dopeui.Note(dopeui.Text(s.Host.Roster.FlagsHint())),
			dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...),
			dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.SaveSubmit()))),
		))
	} else {
		page = append(page, dopeui.Empty(dopeui.Text(s.Host.Roster.TeamsEmpty())))
	}
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// teamFlagsField names one team's Flags input; the save handler reads the id
// back off the name, so the form needs no parallel list of team ids.
func teamFlagsField(teamID int64) string {
	return "flags_" + idstr.Format(teamID)
}

// optionalID renders a rating id, or "" when it is 0 (matching {{if .RatingID}}).
func optionalID(id int64) string {
	if id == 0 {
		return ""
	}
	return idstr.Format(id)
}

// hostPlayersDoc builds the fest players page: the add-override dialog (datalist
// autocomplete + game picker), the overrides table with per-row edit dialogs, and
// the players table. Dialog open/close, the delete confirm, and the datalist →
// hidden-id validation run through pageforms.js / roster.js data-attributes.
func hostPlayersDoc(data hostFestRosterData) *dopeui.Doc {
	s := dopestrings.Default
	ref := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(s.Host.Roster.PlayersTitle(data.Fest.Title)), dopeui.PagePublic, dopeui.Classicscripts("dist/pageforms.js dist/roster.js"),
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(ref, data.Fest.Title), s.Host.Roster.PlayersCrumb())),
	}
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}
	if data.Notice != "" {
		page = append(page, dopeui.Note(dopeui.Text(data.Notice)))
	}
	page = append(page,
		dopeui.Row(dopeui.Button(dopeui.Data("dialog-open", "playerOverrideDialog"), dopeui.Text(s.Host.Roster.AddOverrideBtn()))),
		hostAddOverrideDialog(data, ref),
	)
	if len(data.Overrides) > 0 {
		page = append(page, hostOverridesSection(data, ref))
	}
	if len(data.Players) > 0 {
		rows := []dopeui.Item{dopeui.Trow(dopeui.Hcell(dopeui.Text("ID")), dopeui.Hcell(dopeui.Text(s.Host.Roster.PlayerLabel())), dopeui.Hcell(dopeui.Text(s.Host.Roster.TeamLabel())))}
		for _, p := range data.Players {
			rows = append(rows, dopeui.Trow(dopeui.Cell(dopeui.Text(optionalID(p.RatingID))), dopeui.Cell(dopeui.Text(p.Name)), dopeui.Cell(dopeui.Text(p.Team))))
		}
		page = append(page, dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...))
	} else {
		page = append(page, dopeui.Empty(dopeui.Text(s.Host.Roster.PlayersEmpty())))
	}
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

func hostAddOverrideDialog(data hostFestRosterData, ref string) *dopeui.Element {
	s := dopestrings.Default
	playerOpts := make([]dopeui.Item, 0, len(data.OverridePlayers))
	for _, o := range data.OverridePlayers {
		playerOpts = append(playerOpts, dopeui.Option(dopeui.Value(o.Label), dopeui.Data("id", idstr.Format(o.ID))))
	}
	teamOpts := make([]dopeui.Item, 0, len(data.OverrideTeams))
	for _, o := range data.OverrideTeams {
		teamOpts = append(teamOpts, dopeui.Option(dopeui.Value(o.Label), dopeui.Data("id", idstr.Format(o.ID))))
	}
	var gamePicker dopeui.Item
	if len(data.OverrideGames) > 0 {
		boxes := make([]dopeui.Item, 0, len(data.OverrideGames))
		for _, g := range data.OverrideGames {
			boxes = append(boxes, dopeui.Checkbox(dopeui.Name("game_id"), dopeui.Value(idstr.Format(g.ID)), dopeui.Text(g.Label)))
		}
		gamePicker = dopeui.Col(append([]dopeui.Item{dopeui.SpaceSM}, boxes...)...)
	} else {
		gamePicker = dopeui.Empty(dopeui.Text(s.Host.Roster.NoOverrideGames()))
	}
	return dopeui.Dialog(dopeui.ID("playerOverrideDialog"),
		dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/players/overrides"), dopeui.Autocomplete("off"), dopeui.Data("player-override-form", ""),
			dopeui.Subhead(dopeui.Text(s.Host.Roster.OverrideTitle())),
			dopeui.Hiddenfield(dopeui.Name("player_id"), dopeui.Data("player-override-player-id", "")),
			dopeui.Hiddenfield(dopeui.Name("team_id"), dopeui.Data("player-override-team-id", "")),
			dopeui.Field(dopeui.Label(s.Host.Roster.PlayerLabel()),
				dopeui.Textfield(dopeui.Name("player_label"), dopeui.InputList("playerOverridePlayers"), dopeui.Required(), dopeui.Data("player-override-player", ""))),
			dopeui.Datalist(append([]dopeui.Item{dopeui.ID("playerOverridePlayers")}, playerOpts...)...),
			dopeui.Field(dopeui.Label(s.Host.Roster.NewTeamLabel()),
				dopeui.Textfield(dopeui.Name("team_label"), dopeui.InputList("playerOverrideTeams"), dopeui.Required(), dopeui.Data("player-override-team", ""))),
			dopeui.Datalist(append([]dopeui.Item{dopeui.ID("playerOverrideTeams")}, teamOpts...)...),
			dopeui.Pickgroup(dopeui.Label(s.Host.Roster.GamesLabel()), gamePicker),
			dopeui.Row(dopeui.SpaceSM, dopeui.Wrap(),
				dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.SaveSubmit())),
				dopeui.Button(dopeui.Data("dialog-close", ""), dopeui.Text(s.Host.Roster.CancelBtn())),
			),
		),
	)
}

func hostOverridesSection(data hostFestRosterData, ref string) *dopeui.Element {
	s := dopestrings.Default
	rows := []dopeui.Item{dopeui.Trow(
		dopeui.Hcell(dopeui.Text(s.Host.Roster.PlayerLabel())), dopeui.Hcell(dopeui.Text(s.Host.Roster.ColFromTeam())),
		dopeui.Hcell(dopeui.Text(s.Host.Roster.ColToTeam())), dopeui.Hcell(dopeui.Text(s.Host.Roster.GamesLabel())), dopeui.Hcell(),
	)}
	for _, o := range data.Overrides {
		games := dopeui.Cell(dopeui.Text(o.Games))
		rows = append(rows, dopeui.Trow(
			dopeui.Cell(dopeui.Text(o.Player)), dopeui.Cell(dopeui.Text(o.SourceTeam)),
			dopeui.Cell(dopeui.Text(o.OverrideTeam)), games,
			dopeui.Cell(dopeui.Iconbtn(dopeui.IconPencil, dopeui.Label(s.Host.Roster.EditOverrideLabel()), dopeui.Data("dialog-open", o.DialogID()))),
		))
	}
	sect := []dopeui.Item{
		dopeui.ID("overrides"),
		dopeui.Subhead(dopeui.Text(s.Host.Roster.OverridesSubhead())),
		dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...),
	}
	for _, o := range data.Overrides {
		sect = append(sect, hostOverrideEditDialog(data, ref, o))
	}
	return dopeui.Section(sect...)
}

// overrideGameBoxes is a checkbox per game, ticked for the games o covers.
func overrideGameBoxes(data hostFestRosterData, o overrides.HostPlayerOverrideRow) []dopeui.Item {
	boxes := make([]dopeui.Item, 0, len(data.OverrideGames))
	for _, g := range data.OverrideGames {
		items := []dopeui.Item{dopeui.Name("game_id"), dopeui.Value(idstr.Format(g.ID))}
		if o.HasGame(g.ID) {
			items = append(items, dopeui.Checked())
		}
		boxes = append(boxes, dopeui.Checkbox(append(items, dopeui.Text(g.Label))...))
	}
	return boxes
}

func hostOverrideEditDialog(data hostFestRosterData, ref string, o overrides.HostPlayerOverrideRow) *dopeui.Element {
	s := dopestrings.Default
	boxes := overrideGameBoxes(data, o)
	summary := dopeui.Row(dopeui.SpaceMD, dopeui.Wrap(),
		dopeui.Col(dopeui.SpaceNone, dopeui.Muted(dopeui.Text(s.Host.Roster.PlayerLabel())), dopeui.Strong(dopeui.Text(o.Player))),
		dopeui.Col(dopeui.SpaceNone, dopeui.Muted(dopeui.Text(s.Host.Roster.ColFromTeam())), dopeui.Strong(dopeui.Text(o.SourceTeam))),
		dopeui.Col(dopeui.SpaceNone, dopeui.Muted(dopeui.Text(s.Host.Roster.ColToTeam())), dopeui.Strong(dopeui.Text(o.OverrideTeam))),
	)
	return dopeui.Dialog(dopeui.ID(o.DialogID()),
		dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/players/overrides"), dopeui.Autocomplete("off"),
			dopeui.Subhead(dopeui.Text(s.Host.Roster.OverrideTitle())),
			dopeui.Hiddenfield(dopeui.Name("mode"), dopeui.Value("edit")),
			dopeui.Hiddenfield(dopeui.Name("player_id"), dopeui.Value(idstr.Format(o.PlayerID))),
			dopeui.Hiddenfield(dopeui.Name("source_team_id"), dopeui.Value(idstr.Format(o.SourceTeamID))),
			dopeui.Hiddenfield(dopeui.Name("team_id"), dopeui.Value(idstr.Format(o.OverrideTeamID))),
			summary,
			dopeui.Pickgroup(append([]dopeui.Item{dopeui.Label(s.Host.Roster.GamesLabel())}, dopeui.Col(append([]dopeui.Item{dopeui.SpaceSM}, boxes...)...))...),
			dopeui.Row(dopeui.SpaceSM, dopeui.Wrap(),
				dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.SaveSubmit())),
				dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Name("delete"), dopeui.Value("1"), dopeui.Formnovalidate(),
					dopeui.Data("confirm", s.Host.Roster.DeleteOverrideConfirm()), dopeui.Text(s.Host.Roster.DeleteBtn())),
				dopeui.Button(dopeui.Data("dialog-close", ""), dopeui.Text(s.Host.Roster.CancelBtn())),
			),
		),
	)
}

// hostRatingImportDoc builds the rating.chgk.info roster-import page: when the
// fest has a rating id, a confirm-and-import form; otherwise a note to set one.
// A stopped import adds the reconcile dialog, which the page opens on load.
func hostRatingImportDoc(data hostFestImportData) *dopeui.Doc {
	s := dopestrings.Default
	festRef := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(s.Host.Roster.RatingImportTitle(data.Fest.Title)), dopeui.PagePublic,
		dopeui.Classicscripts("dist/pageforms.js"),
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(festRef, data.Fest.Title), s.Host.Roster.RatingImportCrumb())),
	}
	page = append(page, importMessages(data.Error, data.Notice)...)

	var sect []dopeui.Item
	if data.RatingID != 0 {
		sect = []dopeui.Item{
			dopeui.Note(dopeui.Text(s.Host.Roster.RatingSource(idstr.Format(data.RatingID)))),
			dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+festRef+"/rating/import"), dopeui.Autocomplete("off"),
				dopeui.Note(dopeui.Text(s.Host.Roster.RatingImportNote())),
				dopeui.Hiddenfield(dopeui.Name("preview"), dopeui.Value("1")),
				dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.ImportSubmit()))),
			),
		}
	} else {
		sect = []dopeui.Item{dopeui.Empty(dopeui.Text(s.Host.Roster.NeedRatingNote()))}
	}
	page = append(page, dopeui.Section(sect...))
	if data.Plan != nil {
		page = append(page, hostImportPlanSection(*data.Plan, data.Choice, festRef))
	}
	if data.UndoAt != "" && data.Plan == nil {
		page = append(page, dopeui.Section(
			dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+festRef+"/rating/undo"), dopeui.Autocomplete("off"),
				dopeui.Note(dopeui.Text(s.Host.Roster.UndoNote(snapshotTime(data.UndoAt)))),
				dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.UndoSubmit()))),
			),
		))
	}
	if data.Conflict != nil {
		page = append(page, hostRosterConflictDialog(data.Conflict, festRef))
	}
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// hostImportPlanSection is the preview of an import: what it adds, drops and
// renames, whose people come and go, which of the host's edits it keeps, and
// the players the site moved away from where the host put them, each with a
// box to take the site's placement. Its form confirms the import with the
// same answers.
func hostImportPlanSection(plan imports.ImportPlan, choice imports.RosterChoice, festRef string) *dopeui.Element {
	s := dopestrings.Default
	// The plan's lines stand in one column with one gap between them, so a
	// note and a team's block read as items of the same list.
	var items []dopeui.Item
	if plan.Empty() {
		items = append(items, dopeui.Muted(dopeui.Text(s.Host.Roster.PreviewNothing())))
	}
	if len(plan.AddedTeams) > 0 {
		items = append(items, dopeui.Muted(dopeui.Text(s.Host.Roster.PlanAddedTeams(strings.Join(plan.AddedTeams, ", ")))))
	}
	if len(plan.DroppedTeams) > 0 {
		items = append(items, dopeui.Muted(dopeui.Text(s.Host.Roster.PlanDroppedTeams(strings.Join(plan.DroppedTeams, ", ")))))
	}
	for _, r := range plan.Renamed {
		items = append(items, dopeui.Muted(dopeui.Text(s.Host.Roster.PlanRenamed(r.From, r.To))))
	}
	for _, team := range plan.Players {
		card := []dopeui.Item{dopeui.SpaceNone, dopeui.Strong(dopeui.Text(s.Host.Roster.PlanPlayers(team.Team)))}
		if len(team.Added) > 0 {
			card = append(card, dopeui.Muted(dopeui.Text(s.Host.Roster.PlanPlayersAdded(strings.Join(team.Added, ", ")))))
		}
		if len(team.Removed) > 0 {
			card = append(card, dopeui.Muted(dopeui.Text(s.Host.Roster.PlanPlayersRemoved(strings.Join(team.Removed, ", ")))))
		}
		items = append(items, dopeui.Col(card...))
	}
	var kept []string
	for _, line := range []struct {
		n    int
		text func(int) string
	}{
		{plan.Kept.HandTeams, s.Host.Roster.KeptHandTeams},
		{plan.Kept.Renamed, func(n int) string { return s.Host.Roster.KeptRenamed(strconv.Itoa(n)) }},
		{plan.Kept.Flags, func(n int) string { return s.Host.Roster.KeptFlags(strconv.Itoa(n)) }},
		{plan.Kept.RemovedTeams, func(n int) string { return s.Host.Roster.KeptRemovedTeams(strconv.Itoa(n)) }},
		{plan.Kept.Added, func(n int) string { return s.Host.Roster.KeptAdded(strconv.Itoa(n)) }},
		{plan.Kept.Removed, func(n int) string { return s.Host.Roster.KeptRemoved(strconv.Itoa(n)) }},
	} {
		if line.n > 0 {
			kept = append(kept, line.text(line.n))
		}
	}
	if len(kept) > 0 {
		items = append(items, dopeui.Col(dopeui.SpaceNone,
			dopeui.Strong(dopeui.Text(s.Host.Roster.PlanKeptTitle())),
			dopeui.Muted(dopeui.Text(strings.Join(kept, "; ")))))
	}
	section := []dopeui.Item{dopeui.Subhead(dopeui.Text(s.Host.Roster.PreviewTitle())), dopeui.Col(append([]dopeui.Item{dopeui.SpaceSM}, items...)...)}
	form := []dopeui.Item{dopeui.DirCol, dopeui.SpaceMD, dopeui.Method("post"), dopeui.Action("/host/fest/" + festRef + "/rating/import"), dopeui.Autocomplete("off")}
	for teamID, ratingID := range choice.Merge {
		form = append(form, dopeui.Hiddenfield(dopeui.Name(rosterChoiceField(teamID)), dopeui.Value(rosterChoiceMerge(ratingID))))
	}
	for teamID := range choice.Drop {
		form = append(form, dopeui.Hiddenfield(dopeui.Name(rosterChoiceField(teamID)), dopeui.Value(rosterChoiceDrop)))
	}
	if len(plan.Conflicts) > 0 {
		boxes := []dopeui.Item{dopeui.SpaceSM}
		for _, c := range plan.Conflicts {
			player := store.JoinPlayerName(c.Player.FirstName, c.Player.LastName)
			box := []dopeui.Item{dopeui.Name(conflictField(c.Key)), dopeui.Value("1"),
				dopeui.Text(s.Host.Roster.ConflictPlayer(player, c.HandTeam, c.SiteTeam) + " · " + s.Host.Roster.ConflictTakeSite())}
			if choice.AcceptSite[c.Key] {
				box = append(box, dopeui.Checked())
			}
			boxes = append(boxes, dopeui.Checkbox(box...))
		}
		form = append(form,
			dopeui.Strong(dopeui.Text(s.Host.Roster.ConflictsTitle())),
			dopeui.Note(dopeui.Text(s.Host.Roster.ConflictsHint())),
			dopeui.Col(boxes...))
	}
	if !plan.Empty() {
		form = append(form, dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.ConfirmSubmit()))))
		section = append(section, dopeui.Form(form...))
	}
	return dopeui.Section(section...)
}

// hostRosterConflictDialog asks, per team that is leaving the roster with
// results on it, whether it is one of the teams the roster brings (a changed
// id, which keeps the number and everything scored under it) or a team that
// really withdrew (which loses both). Nothing is preselected except the
// incoming team of the same name, which is what a changed id looks like.
func hostRosterConflictDialog(conflict *imports.RosterConflict, festRef string) *dopeui.Element {
	s := dopestrings.Default
	form := []dopeui.Item{
		dopeui.DirCol, dopeui.SpaceMD, dopeui.Method("post"), dopeui.Action("/host/fest/" + festRef + "/rating/import"), dopeui.Autocomplete("off"),
		dopeui.Subhead(dopeui.Text(s.Host.Roster.ConflictTitle())),
		dopeui.Note(dopeui.Text(s.Host.Roster.ConflictHint())),
		// Answered, the import is previewed first, like any other.
		dopeui.Hiddenfield(dopeui.Name("preview"), dopeui.Value("1")),
	}
	for _, team := range conflict.Dropped {
		form = append(form, hostRosterConflictTeam(team, conflict.Added))
	}
	form = append(form, dopeui.Row(dopeui.SpaceSM, dopeui.Wrap(),
		dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.ConflictSubmit())),
		dopeui.Button(dopeui.Data("dialog-close", ""), dopeui.Text(s.Host.Roster.ConflictCancel())),
	))
	return dopeui.Dialog(dopeui.ID("rosterConflictDialog"), dopeui.Data("dialog-auto", ""), dopeui.Form(form...))
}

func hostRosterConflictTeam(team imports.DroppedTeam, added []imports.AddedTeam) *dopeui.Element {
	s := dopestrings.Default
	field := rosterChoiceField(team.TeamID)
	card := []dopeui.Item{
		dopeui.DirCol, dopeui.SpaceSM,
		dopeui.Strong(dopeui.Text(s.Host.Roster.ConflictTeam(idstr.Format(team.Number), teamDisplayName(team.Name, team.City)))),
		dopeui.Muted(dopeui.Text(s.Host.Roster.ConflictGames(strings.Join(team.Games, ", ")))),
	}
	if len(added) == 0 {
		return dopeui.Card(append(card,
			dopeui.Note(dopeui.Text(s.Host.Roster.ConflictNoCandidates())),
			dopeui.Checkbox(dopeui.Name(field), dopeui.Value(rosterChoiceDrop), dopeui.Text(s.Host.Roster.ConflictChoiceDrop())),
		)...)
	}
	choices := []dopeui.Item{dopeui.SpaceSM}
	for _, candidate := range added {
		items := []dopeui.Item{dopeui.Name(field), dopeui.Value(rosterChoiceMerge(candidate.RatingID)),
			dopeui.Text(s.Host.Roster.ConflictChoiceMerge(idstr.Format(candidate.RatingID), teamDisplayName(candidate.Name, candidate.City)))}
		// Nothing is preselected but the incoming team of the same name: a team
		// whose id changed keeps its name, which is the case this dialog exists for.
		if candidate.Name == team.Name {
			items = append(items, dopeui.Checked())
		}
		choices = append(choices, dopeui.Radio(items...))
	}
	choices = append(choices, dopeui.Radio(dopeui.Name(field), dopeui.Value(rosterChoiceDrop), dopeui.Text(s.Host.Roster.ConflictChoiceDrop())))
	return dopeui.Card(append(card,
		dopeui.Pickgroup(dopeui.Label(s.Host.Roster.ConflictChoiceLabel()), dopeui.Col(choices...)),
	)...)
}

// teamDisplayName is the fest's way of naming a team to a person, its name
// with its city in brackets, which the numbering page already uses.
func teamDisplayName(name, city string) string {
	return numbering.DisplayName(numbering.Team{Name: name, City: city})
}

// The reconcile form names one field per conflicted team, carrying either the
// rating id it is merged into or the word that agrees to lose it.
const rosterChoiceDrop = "drop"

func rosterChoiceField(teamID int64) string {
	return "team_" + idstr.Format(teamID)
}

func rosterChoiceMerge(ratingID int64) string {
	return "merge:" + idstr.Format(ratingID)
}

// parseRosterChoice reads the reconcile form back into the answer the import
// takes. An unanswered team is simply absent, and the import asks again.
func parseRosterChoice(form url.Values) imports.RosterChoice {
	choice := imports.RosterChoice{Merge: map[int64]int64{}, Drop: map[int64]bool{}, AcceptSite: map[string]bool{}}
	for key, values := range form {
		if conflict, ok := strings.CutPrefix(key, "accept_"); ok && len(values) > 0 && values[0] == "1" {
			choice.AcceptSite[conflict] = true
			continue
		}
		teamText, ok := strings.CutPrefix(key, "team_")
		if !ok || len(values) == 0 {
			continue
		}
		teamID, err := idstr.Parse(teamText)
		if err != nil || teamID <= 0 {
			continue
		}
		if values[0] == rosterChoiceDrop {
			choice.Drop[teamID] = true
			continue
		}
		ratingText, ok := strings.CutPrefix(values[0], "merge:")
		if !ok {
			continue
		}
		if ratingID, err := idstr.Parse(ratingText); err == nil && ratingID > 0 {
			choice.Merge[teamID] = ratingID
		}
	}
	return choice
}

// importMessages renders the shared error (empty) + notice (muted) lines the
// import pages show above their forms.
func importMessages(errMsg, notice string) []dopeui.Item {
	var out []dopeui.Item
	if errMsg != "" {
		out = append(out, dopeui.Empty(dopeui.Text(errMsg)))
	}
	if notice != "" {
		out = append(out, dopeui.Note(dopeui.Text(notice)))
	}
	return out
}

func (s *Server) renderHostFestTeams(w http.ResponseWriter, r *http.Request, festID int64) {
	s.renderHostFestTeamsWithMessage(w, r, festID, "", "")
}

func (s *Server) renderHostFestTeamsWithMessage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
		teams, err := s.loadHostFestTeams(r.Context(), festID)
		if err != nil {
			return nil, err
		}
		return hostTeamsDoc(hostFestRosterData{Fest: fest, Teams: teams, Error: errMsg, Notice: notice}), nil
	})
}

// handleHostSaveFestTeamFlags saves the Flags typed on the teams page. It goes
// down the same road a rating import does — rewrite the fest's Flags, fold the
// roster into every flat Protocol's document, bump the revision, broadcast —
// so a Division appears on the game pages without a reload.
func (s *Server) handleHostSaveFestTeamFlags(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	teams, err := s.loadHostFestTeams(r.Context(), festID)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	typed := make(map[int64][]roster.FestRosterFlag, len(teams))
	for _, team := range teams {
		typed[team.ID] = parseTypedFlags(r.Form.Get(teamFlagsField(team.ID)))
	}
	if err := s.saveFestTeamFlags(r.Context(), festID, typed); err != nil {
		s.renderHostFestTeamsWithMessage(w, r, festID, err.Error(), "")
		return
	}
	s.renderHostFestTeamsWithMessage(w, r, festID, "", dopestrings.Default.Host.Roster.FlagsSavedNotice())
}

// parseTypedFlags reads one team's Flags as an organizer types them: short
// names separated by commas. A hand-typed Flag has no rating id and no separate
// full name — the short name is all there is, so it is both.
func parseTypedFlags(value string) []roster.FestRosterFlag {
	var flags []roster.FestRosterFlag
	for _, part := range strings.Split(value, ",") {
		flags = append(flags, roster.FestRosterFlag{Short: part, Full: part})
	}
	return roster.NormalizeFlags(flags)
}

// SaveTeamFlags sets the Flags of the named teams, each typed as the teams
// page takes them: short names separated by commas. Teams left out keep theirs.
func (s *Server) SaveTeamFlags(ctx context.Context, festID int64, typed map[int64]string) error {
	teams, err := s.loadHostFestTeams(ctx, festID)
	if err != nil {
		return err
	}
	known := make(map[int64]bool, len(teams))
	for _, team := range teams {
		known[team.ID] = true
	}
	flags := make(map[int64][]roster.FestRosterFlag, len(typed))
	for teamID, value := range typed {
		if !known[teamID] {
			return corei18n.User(dopestrings.Default.Host.Roster.ErrorFlagsForeignTeam(idstr.Format(teamID)))
		}
		flags[teamID] = parseTypedFlags(value)
	}
	return s.saveFestTeamFlags(ctx, festID, flags)
}

// saveFestTeamFlags commits the Flags typed for the named teams
// (entrants.SaveTeamFlagsTx), which tells the game pages they changed.
func (s *Server) saveFestTeamFlags(ctx context.Context, festID int64, flagsByTeam map[int64][]roster.FestRosterFlag) error {
	_, err := s.commit(ctx, festID, "fest-team-flags", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		return entrants.SaveTeamFlagsTx(ctx, tx, festID, flagsByTeam)
	})
	return err
}

func (s *Server) renderHostFestPlayers(w http.ResponseWriter, r *http.Request, festID int64) {
	s.renderHostFestPlayersWithMessage(w, r, festID, "", "")
}

func (s *Server) renderHostFestPlayersWithMessage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
		players, err := s.loadHostFestPlayers(r.Context(), festID)
		if err != nil {
			return nil, err
		}
		overridePlayers, overrideTeams, overrideGames, overrides, err := overrides.LoadHostPlayerOverrideOptions(r.Context(), s.h.Engine().DB, festID)
		if err != nil {
			return nil, err
		}
		return hostPlayersDoc(hostFestRosterData{
			Fest:            fest,
			Players:         players,
			OverridePlayers: overridePlayers,
			OverrideTeams:   overrideTeams,
			OverrideGames:   overrideGames,
			Overrides:       overrides,
			Error:           errMsg,
			Notice:          notice,
		}), nil
	})
}

// AddPlayerOverride moves a player to another team for the given games (none
// means every game) and tells the bracket pages their rosters changed.
func (s *Server) AddPlayerOverride(ctx context.Context, festID, playerID, teamID int64, gameIDs []int64) error {
	revision, ekGameIDs, err := overrides.SavePlayerTeamOverride(s.h.Engine(), ctx, festID, playerID, teamID, gameIDs)
	if err != nil {
		return err
	}
	s.fanOut(festID, revision, core.Broadcast{Rosters: ekGameIDs})
	return nil
}

// ReplacePlayerOverride rewrites the override that moved playerID from
// sourceTeamID. A nil gameIDs deletes it.
func (s *Server) ReplacePlayerOverride(ctx context.Context, festID, playerID, sourceTeamID, teamID int64, gameIDs []int64) error {
	revision, ekGameIDs, err := overrides.ReplacePlayerTeamOverride(s.h.Engine(), ctx, festID, playerID, sourceTeamID, teamID, gameIDs)
	if err != nil {
		return err
	}
	s.fanOut(festID, revision, core.Broadcast{Rosters: ekGameIDs})
	return nil
}

func (s *Server) handleHostAddPlayerOverride(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("mode") == "edit" || r.Form.Get("delete") == "1" {
		s.handleHostEditPlayerOverride(w, r, festID)
		return
	}
	playerID, err := overrides.ParseHostOverrideID(r.Form.Get("player_id"), dopestrings.Default.Host.Roster.ErrorObjPlayer())
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	teamID, err := overrides.ParseHostOverrideID(r.Form.Get("team_id"), dopestrings.Default.Host.Roster.ErrorObjTeam())
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	gameIDs, err := overrides.ParseHostOverrideGameIDs(r.Form["game_id"])
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	if err := s.AddPlayerOverride(r.Context(), festID, playerID, teamID, gameIDs); err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/players#overrides", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

func (s *Server) handleHostEditPlayerOverride(w http.ResponseWriter, r *http.Request, festID int64) {
	playerID, err := overrides.ParseHostOverrideID(r.Form.Get("player_id"), dopestrings.Default.Host.Roster.ErrorObjPlayer())
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	sourceTeamID, err := overrides.ParseHostOverrideID(r.Form.Get("source_team_id"), dopestrings.Default.Host.Roster.ErrorObjSourceTeam())
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	teamID, err := overrides.ParseHostOverrideID(r.Form.Get("team_id"), dopestrings.Default.Host.Roster.ErrorObjTeam())
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	var gameIDs []int64
	if r.Form.Get("delete") != "1" {
		gameIDs, err = overrides.ParseHostOverrideGameIDs(r.Form["game_id"])
		if err != nil {
			s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
			return
		}
	}
	if err := s.ReplacePlayerOverride(r.Context(), festID, playerID, sourceTeamID, teamID, gameIDs); err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/players#overrides", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

func (s *Server) renderHostRatingImportPage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	s.renderHostRatingImport(w, r, festID, errMsg, notice, nil)
}

func (s *Server) renderHostRatingImport(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string, conflict *imports.RosterConflict) {
	s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
		ratingID, err := s.loadFestRatingID(r.Context(), festID)
		if err != nil {
			return nil, err
		}
		undoAt, err := imports.LastRosterSnapshot(r.Context(), s.h.Engine().DB, festID)
		if err != nil {
			return nil, err
		}
		return hostRatingImportDoc(hostFestImportData{Fest: fest, RatingID: ratingID, Error: errMsg, Notice: notice, Conflict: conflict, UndoAt: undoAt}), nil
	})
}

func (s *Server) loadHostFestTeams(ctx context.Context, festID int64) ([]hostFestTeam, error) {
	teams, err := store.CollectRows(ctx, s.h.Engine().DB, `
select tt.id, coalesce(tt.rating_id, 0), tt.name, tt.city, count(ttp.player_id)
from fest_teams tt
left join fest_team_players ttp on ttp.team_id = tt.id
where tt.fest_id = ? and tt.deleted = 0
group by tt.id
order by tt.position, tt.id`, []any{festID}, func(rows *sql.Rows) (hostFestTeam, error) {
		var team hostFestTeam
		if err := rows.Scan(&team.ID, &team.RatingID, &team.Name, &team.City, &team.Players); err != nil {
			return team, err
		}
		return team, nil
	})
	if err != nil {
		return nil, err
	}
	flags, err := roster.LoadFestTeamFlags(ctx, s.h.Engine().DB, festID)
	if err != nil {
		return nil, err
	}
	state, err := roster.LoadHandState(ctx, s.h.Engine().DB, festID)
	if err != nil {
		return nil, err
	}
	edited := editedTeams(state)
	hand := map[int64]bool{}
	for _, team := range state.Teams {
		hand[team.ID] = team.Hand
	}
	for i := range teams {
		teams[i].Flags = strings.Join(roster.FlagShortNames(flags[teams[i].ID]), ", ")
		teams[i].Hand, teams[i].Edited = hand[teams[i].ID], edited[teams[i].ID]
	}
	sort.SliceStable(teams, func(i, j int) bool {
		if cmp := util.CompareAlpha(teams[i].Name, teams[j].Name); cmp != 0 {
			return cmp < 0
		}
		if cmp := util.CompareAlpha(teams[i].City, teams[j].City); cmp != 0 {
			return cmp < 0
		}
		return teams[i].RatingID < teams[j].RatingID
	})
	return teams, nil
}

func (s *Server) loadHostFestPlayers(ctx context.Context, festID int64) ([]hostFestPlayer, error) {
	players, err := store.CollectRows(ctx, s.h.Engine().DB, `
select coalesce(p.rating_id, 0), p.first_name, p.last_name, tt.name
from fest_team_players ttp
join fest_players p on p.id = ttp.player_id
join fest_teams tt on tt.id = ttp.team_id
where tt.fest_id = ? and tt.deleted = 0
order by tt.position, tt.id, ttp.roster_order, p.id`, []any{festID}, func(rows *sql.Rows) (hostFestPlayer, error) {
		var firstName, lastName, teamName string
		var ratingID int64
		if err := rows.Scan(&ratingID, &firstName, &lastName, &teamName); err != nil {
			return hostFestPlayer{}, err
		}
		return hostFestPlayer{
			RatingID: ratingID,
			Name:     store.JoinPlayerName(firstName, lastName),
			Team:     teamName,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	sortHostFestPlayers(players)
	return players, nil
}

// sortHostFestPlayers orders players by team, then name, then rating id.
func sortHostFestPlayers(players []hostFestPlayer) {
	sort.SliceStable(players, func(i, j int) bool {
		if cmp := util.CompareAlpha(players[i].Team, players[j].Team); cmp != 0 {
			return cmp < 0
		}
		if cmp := util.CompareAlpha(players[i].Name, players[j].Name); cmp != 0 {
			return cmp < 0
		}
		return players[i].RatingID < players[j].RatingID
	})
}

// ImportRatingRoster pulls the fest's roster from rating.chgk.info. A
// *imports.RosterConflict means teams with results would be lost, and the
// caller answers it with a choice and calls again. The Troika Games that
// follow a division are re-seated in the import's transaction
// (entrants.ImportFestRoster); their open pages are told here.
func (s *Server) ImportRatingRoster(ctx context.Context, festID int64, choice imports.RosterChoice) (imports.RatingRosterImportResult, error) {
	ratingID, err := s.loadFestRatingID(ctx, festID)
	if err != nil {
		return imports.RatingRosterImportResult{}, err
	}
	if ratingID <= 0 {
		return imports.RatingRosterImportResult{}, corei18n.User(dopestrings.Default.Host.Roster.NeedRatingNote())
	}
	teams, err := imports.FetchRatingRoster(s.h.Engine(), ctx, ratingID)
	if err != nil {
		return imports.RatingRosterImportResult{}, err
	}
	result, err := entrants.ImportFestRoster(s.h.Engine(), ctx, festID, ratingID, teams, choice)
	if err != nil || result.Revision == 0 {
		return result, err
	}
	s.fanOut(festID, result.Revision, core.Broadcast{Views: result.Views})
	return result, nil
}

func (s *Server) handleHostImportRatingRoster(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ratingID, err := s.loadFestRatingID(r.Context(), festID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		route.WriteError(w, r, err)
		return
	}
	if ratingID <= 0 {
		s.renderHostRatingImportPage(w, r, festID, dopestrings.Default.Host.Roster.NeedRatingNote(), "")
		return
	}
	choice := parseRosterChoice(r.Form)
	choice.Preview = r.Form.Get("preview") == "1"
	result, err := s.ImportRatingRoster(r.Context(), festID, choice)
	var conflict *imports.RosterConflict
	if errors.As(err, &conflict) {
		s.renderHostRatingImport(w, r, festID, "", "", conflict)
		return
	}
	if err != nil {
		s.renderHostRatingImportPage(w, r, festID, err.Error(), "")
		return
	}
	if choice.Preview {
		s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
			return hostRatingImportDoc(hostFestImportData{Fest: fest, RatingID: ratingID, Plan: result.Plan, Choice: choice}), nil
		})
		return
	}
	var msg string
	if result.Unchanged {
		msg = dopestrings.Default.Host.Roster.ImportUnchangedNotice(strconv.Itoa(result.TeamCount), strconv.Itoa(result.PlayerCount))
	} else {
		msg = dopestrings.Default.Host.Roster.ImportDoneCounts(strconv.Itoa(result.TeamCount), strconv.Itoa(result.PlayerCount), strconv.Itoa(result.ODGameCount), strconv.Itoa(result.KSIGameCount))
	}
	if len(choice.Merge) > 0 {
		msg += " " + dopestrings.Default.Host.Roster.ImportMergedNotice(strconv.Itoa(len(choice.Merge)))
	}
	s.renderHostRatingImportPage(w, r, festID, "", msg)
}

// handleHostUndoRatingImport puts the roster back as it was before the last
// import (ADR-0024).
func (s *Server) handleHostUndoRatingImport(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := s.UndoRosterImport(r.Context(), festID); err != nil {
		s.renderHostRatingImportPage(w, r, festID, err.Error(), "")
		return
	}
	s.renderHostRatingImportPage(w, r, festID, "", dopestrings.Default.Host.Roster.UndoDoneNotice())
}

// snapshotTime prints when a roster was saved as a person reads it,
// «29.09 23:03 UTC», falling back to what is stored.
func snapshotTime(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return at
	}
	return t.UTC().Format("02.01 15:04") + " UTC"
}
