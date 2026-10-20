package pages

import (
	"context"
	"database/sql"
	"dope/dope/domain/core"
	"dope/dope/domain/multifest"
	"dope/dope/domain/numbering"
	"dope/dope/domain/roster"
	"dope/dope/domain/view"
	"dope/dope/platform/util"
	ui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"dope/dope/web/route"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

type hostFestNumberRow struct {
	Index     int
	Number    string
	TeamID    int64
	TeamLabel string
}

type hostFestNumbersData struct {
	Fest            view.HostFest
	MultiFestVenues []multifest.Venue
	Venue           int64
	Rows            []hostFestNumberRow
	Error           string
	Notice          string
	HasNumbers      bool
}

// hostNumbersDoc builds the fest team-numbers editor: the read-only number list
// with the actions cluster (clear/auto/import/edit). The edit-in-place toggle and
// the two mass-import <dialog> modals are driven by numbers.js (keyed on the ids
// and data-has-numbers here); the DSL page carries no script.
func hostNumbersDoc(data hostFestNumbersData) *ui.Doc {
	ref := data.Fest.Ref()
	s := dopestrings.Default
	page := []ui.Item{
		ui.Title(s.Numbers.Page.Title(data.Fest.Title)), ui.PagePublic, ui.Classicscripts("dist/pageforms.js dist/numbers.js"),
		ui.Publictopbar(Trail(FestCrumbs(ref, data.Fest.Title), s.Numbers.Page.Crumb())),
	}
	base := "/host/fest/" + ref + "/numbers"
	query := ""
	if data.Venue > 0 {
		query = "?venue=" + idstr.Format(data.Venue)
		if len(data.MultiFestVenues) > 1 {
			page = append(page, VenuePicker(base, data.MultiFestVenues, data.Venue, ""))
		}
	}
	if data.Error != "" {
		page = append(page, ui.Empty(ui.Text(data.Error)))
	}
	if data.Notice != "" {
		page = append(page, ui.Note(ui.Text(data.Notice)))
	}
	if len(data.Rows) == 0 {
		page = append(page, ui.Empty(ui.Text(s.Numbers.Page.Empty())))
		return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
	}

	hasNum := ""
	if data.HasNumbers {
		hasNum = "1"
	}
	actions := []ui.Item{ui.Wrap()}
	if data.HasNumbers {
		actions = append(actions, ui.Button(ui.Submit(), ui.ID("numbers-clear-btn"),
			ui.Formaction(base+"/clear"+query), ui.Formnovalidate(), ui.Text(s.Numbers.Action.Clear())))
	}
	actions = append(actions,
		ui.Button(ui.Submit(), ui.ID("numbers-auto-btn"), ui.Formaction(base+"/auto"+query), ui.Formnovalidate(), ui.Text(s.Numbers.Action.Auto())),
		ui.Button(ui.ID("numbers-import-btn"), ui.Text(s.Numbers.Action.Import())),
	)
	if data.HasNumbers {
		actions = append(actions, ui.Button(ui.ID("numbers-edit-btn"), ui.Text(s.Numbers.Action.Replace())))
	}

	rows := make([]ui.Item, 0, len(data.Rows))
	for _, row := range data.Rows {
		teamID := ""
		if row.TeamID != 0 {
			teamID = idstr.Format(row.TeamID)
		}
		rows = append(rows, ui.Numberrow(
			ui.Index(strconv.Itoa(row.Index)), ui.Num(row.Number),
			ui.Teamlabel(row.TeamLabel), ui.Teamid(teamID),
		))
	}

	form := []ui.Item{ui.ID("numbers-form"), ui.Action(base + query), ui.Data("has-numbers", hasNum)}
	form = append(form,
		ui.Row(actions...),
		ui.Note(ui.ID("numbers-help"), ui.Hidden(), ui.Text(s.Numbers.Help.Edit())),
		ui.Numberlist(rows...),
		ui.Row(ui.ID("numbers-save"), ui.Hidden(),
			ui.Button(ui.Submit(), ui.Text(s.Numbers.Action.Save())),
			ui.Button(ui.ID("numbers-cancel-btn"), ui.Text(s.Numbers.Action.Cancel())),
		),
	)
	page = append(page, ui.Numbersform(form...))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func (s *Server) RenderHostFestNumbers(w http.ResponseWriter, r *http.Request, sc route.Scope, errMsg, notice string, override []hostFestNumberRow) {
	festID := sc.FestID
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	venue, list, err := s.numbersVenue(r, sc)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	data, err := s.buildHostFestNumbersData(r.Context(), festID, venue, errMsg, notice, override)
	data.MultiFestVenues, data.Venue = list, venue
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	data.Fest = fest
	RenderDoc(w, s.h.Engine().AssetETags, hostNumbersDoc(data))
}

func (s *Server) buildHostFestNumbersData(ctx context.Context, festID, multiFestVenueID int64, errMsg, notice string, override []hostFestNumberRow) (hostFestNumbersData, error) {
	teams, err := numbering.LoadTeams(ctx, s.h.Engine().DB, festID, multiFestVenueID)
	if err != nil {
		return hostFestNumbersData{}, err
	}
	data := hostFestNumbersData{Error: errMsg, Notice: notice}
	if len(teams) == 0 {
		return data, nil
	}

	if len(override) > 0 {
		data.Rows = override
	} else {
		data.Rows = defaultNumberRows(teams)
	}
	for _, row := range data.Rows {
		if strings.TrimSpace(row.Number) != "" {
			data.HasNumbers = true
			break
		}
	}
	return data, nil
}

// defaultNumberRows builds one row per team: numbered teams first (sorted by
// number ascending), then unnumbered teams (alphabetical). Unnumbered teams
// get an empty number cell — the host has to explicitly trigger numbering via
// the auto-assign action or by editing a row.
func defaultNumberRows(teams []numbering.Team) []hostFestNumberRow {
	numbered := make([]numbering.Team, 0, len(teams))
	unnumbered := make([]numbering.Team, 0, len(teams))
	for _, team := range teams {
		if team.Number > 0 {
			numbered = append(numbered, team)
		} else {
			unnumbered = append(unnumbered, team)
		}
	}
	sort.SliceStable(numbered, func(i, j int) bool { return numbered[i].Number < numbered[j].Number })
	sort.SliceStable(unnumbered, func(i, j int) bool {
		if cmp := util.CompareAlpha(unnumbered[i].Name, unnumbered[j].Name); cmp != 0 {
			return cmp < 0
		}
		if cmp := util.CompareAlpha(unnumbered[i].City, unnumbered[j].City); cmp != 0 {
			return cmp < 0
		}
		return unnumbered[i].ID < unnumbered[j].ID
	})
	rows := make([]hostFestNumberRow, 0, len(teams))
	for i, team := range numbered {
		rows = append(rows, hostFestNumberRow{
			Index:     i + 1,
			Number:    strconv.Itoa(team.Number),
			TeamID:    team.ID,
			TeamLabel: numbering.DisplayName(team),
		})
	}
	for i, team := range unnumbered {
		rows = append(rows, hostFestNumberRow{
			Index:     len(numbered) + i + 1,
			Number:    "",
			TeamID:    team.ID,
			TeamLabel: numbering.DisplayName(team),
		})
	}
	return rows
}

type parsedNumberRow struct {
	Index    int
	NumRaw   string
	LabelRaw string
	TeamID   int64
}

func parseNumberRowsFromForm(r *http.Request, rowCount int) []parsedNumberRow {
	rows := make([]parsedNumberRow, 0, rowCount)
	for i := 1; i <= rowCount; i++ {
		numRaw := r.Form.Get(fmt.Sprintf("num_%d", i))
		labelRaw := r.Form.Get(fmt.Sprintf("team_label_%d", i))
		idRaw := strings.TrimSpace(r.Form.Get(fmt.Sprintf("team_id_%d", i)))
		var teamID int64
		if idRaw != "" {
			if v, err := idstr.Parse(idRaw); err == nil && v > 0 {
				teamID = v
			}
		}
		rows = append(rows, parsedNumberRow{Index: i, NumRaw: numRaw, LabelRaw: labelRaw, TeamID: teamID})
	}
	return rows
}

func (rows parsedNumberRowSlice) override() []hostFestNumberRow {
	out := make([]hostFestNumberRow, len(rows))
	for i, row := range rows {
		out[i] = hostFestNumberRow{
			Index:     row.Index,
			Number:    strings.TrimSpace(row.NumRaw),
			TeamID:    row.TeamID,
			TeamLabel: strings.TrimSpace(row.LabelRaw),
		}
	}
	return out
}

type parsedNumberRowSlice []parsedNumberRow

func (s *Server) HandleHostSaveFestNumbers(w http.ResponseWriter, r *http.Request, sc route.Scope) {
	festID := sc.FestID
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	venue, _, err := s.numbersVenue(r, sc)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	teams, err := numbering.LoadTeams(r.Context(), s.h.Engine().DB, festID, venue)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	if len(teams) == 0 {
		s.RenderHostFestNumbers(w, r, sc, dopestrings.Default.Numbers.Page.NoTeams(), "", nil)
		return
	}
	validIDs := make(map[int64]bool, len(teams))
	for _, team := range teams {
		validIDs[team.ID] = true
	}
	rows := parseNumberRowsFromForm(r, len(teams))
	override := parsedNumberRowSlice(rows).override()

	numberToRow := make(map[int]int)
	teamToRow := make(map[int64]int)
	assignments := make(map[int64]int, len(teams))

	for _, row := range rows {
		numTxt := strings.TrimSpace(row.NumRaw)
		hasNum := numTxt != ""
		hasTeam := row.TeamID != 0
		if !hasTeam {
			if hasNum {
				s.RenderHostFestNumbers(w, r, sc, dopestrings.Default.Numbers.Error.RowNoTeam(strconv.Itoa(row.Index)), "", override)
				return
			}
			continue
		}
		if !validIDs[row.TeamID] {
			s.RenderHostFestNumbers(w, r, sc, dopestrings.Default.Numbers.Error.RowForeignTeam(strconv.Itoa(row.Index)), "", override)
			return
		}
		if prev, ok := teamToRow[row.TeamID]; ok {
			s.RenderHostFestNumbers(w, r, sc, dopestrings.Default.Numbers.Error.TeamTwoRows(strconv.Itoa(prev), strconv.Itoa(row.Index)), "", override)
			return
		}
		teamToRow[row.TeamID] = row.Index
		if !hasNum {
			// Team intentionally left without a number — keep it unnumbered.
			continue
		}
		n, err := strconv.Atoi(numTxt)
		if err != nil || n <= 0 || n > numbering.MaxNumber {
			s.RenderHostFestNumbers(w, r, sc, dopestrings.Default.Numbers.Error.RowRange(strconv.Itoa(row.Index), strconv.Itoa(numbering.MaxNumber)), "", override)
			return
		}
		if prev, ok := numberToRow[n]; ok {
			s.RenderHostFestNumbers(w, r, sc, dopestrings.Default.Numbers.Error.NumberTwoRows(strconv.Itoa(n), strconv.Itoa(prev), strconv.Itoa(row.Index)), "", override)
			return
		}
		numberToRow[n] = row.Index
		assignments[row.TeamID] = n
	}

	if err := s.SaveFestNumbers(r.Context(), festID, venue, assignments); err != nil {
		s.RenderHostFestNumbers(w, r, sc, err.Error(), "", override)
		return
	}
	notice := dopestrings.Default.Numbers.Notice.Saved()
	if len(assignments) < len(teams) {
		notice = dopestrings.Default.Numbers.Notice.SavedPartial(strconv.Itoa(len(teams) - len(assignments)))
	}
	s.RenderHostFestNumbers(w, r, sc, "", notice, nil)
}

// AutoFestNumbers numbers every team of the fest, or of one venue, 1…n in
// alphabetical order, after dropping the teams that left the roster, whose
// archived numbers would otherwise block reuse.
func (s *Server) AutoFestNumbers(ctx context.Context, festID, multiFestVenueID int64) error {
	if err := s.purgeFestSoftDeletedTeams(ctx, festID); err != nil {
		return err
	}
	teams, err := numbering.LoadTeams(ctx, s.h.Engine().DB, festID, multiFestVenueID)
	if err != nil {
		return err
	}
	if len(teams) == 0 {
		return corei18n.User(dopestrings.Default.Numbers.Page.NoTeams())
	}
	sorted := append([]numbering.Team(nil), teams...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if cmp := util.CompareAlpha(sorted[i].Name, sorted[j].Name); cmp != 0 {
			return cmp < 0
		}
		if cmp := util.CompareAlpha(sorted[i].City, sorted[j].City); cmp != 0 {
			return cmp < 0
		}
		return sorted[i].ID < sorted[j].ID
	})
	assignments := make(map[int64]int, len(sorted))
	for i, team := range sorted {
		assignments[team.ID] = i + 1
	}
	return s.SaveFestNumbers(ctx, festID, multiFestVenueID, assignments)
}

// ClearFestNumbers takes every team's number away, in the fest or one venue.
func (s *Server) ClearFestNumbers(ctx context.Context, festID, multiFestVenueID int64) error {
	if err := s.purgeFestSoftDeletedTeams(ctx, festID); err != nil {
		return err
	}
	return s.SaveFestNumbers(ctx, festID, multiFestVenueID, nil)
}

func (s *Server) HandleHostAutoFestNumbers(w http.ResponseWriter, r *http.Request, sc route.Scope) {
	festID := sc.FestID
	venue, _, err := s.numbersVenue(r, sc)
	if err == nil {
		err = s.AutoFestNumbers(r.Context(), festID, venue)
	}
	if err != nil {
		s.RenderHostFestNumbers(w, r, sc, err.Error(), "", nil)
		return
	}
	s.RenderHostFestNumbers(w, r, sc, "", dopestrings.Default.Numbers.Notice.Auto(), nil)
}

func (s *Server) HandleHostClearFestNumbers(w http.ResponseWriter, r *http.Request, sc route.Scope) {
	festID := sc.FestID
	venue, _, err := s.numbersVenue(r, sc)
	if err == nil {
		err = s.ClearFestNumbers(r.Context(), festID, venue)
	}
	if err != nil {
		s.RenderHostFestNumbers(w, r, sc, err.Error(), "", nil)
		return
	}
	s.RenderHostFestNumbers(w, r, sc, "", dopestrings.Default.Numbers.Notice.Cleared(), nil)
}

// purgeFestSoftDeletedTeams hard-deletes any soft-deleted fest_teams rows for
// this fest. Used by the "assign numbers" and "clear" actions, which the host
// has explicitly confirmed as destructive resets — archived numbers from participants
// that left the roster must not block reuse of those numbers.
func (s *Server) purgeFestSoftDeletedTeams(reqCtx context.Context, festID int64) error {
	return s.h.Engine().WithWriteTx(reqCtx, festID, "purge-soft-deleted-teams", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from fest_teams where fest_id = ? and deleted = 1`, festID)
		return err
	})
}

// SaveFestNumbers numbers the fest's teams, or one venue's under the venue's
// own numbers, as assignments says; a team left out has none.
func (s *Server) SaveFestNumbers(reqCtx context.Context, festID, multiFestVenueID int64, assignments map[int64]int) error {
	column, scope, args := "number", "fest_id = ?", []any{festID}
	if multiFestVenueID > 0 {
		column, scope, args = "venue_number", "fest_id = ? and multifest_venue_id = ?", []any{festID, multiFestVenueID}
	}
	_, err := s.Commit(reqCtx, festID, "fest-numbers", nil, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		oldTeams, err := numbering.LoadTeams(ctx, tx, festID, multiFestVenueID)
		if err != nil {
			return core.FestWrite{}, err
		}
		oldByID := make(map[int64]int, len(oldTeams))
		for _, team := range oldTeams {
			oldByID[team.ID] = team.Number
		}
		entryRemap := make(map[int]int)
		for teamID, newNum := range assignments {
			oldNum := oldByID[teamID]
			if oldNum > 0 && oldNum != newNum {
				entryRemap[oldNum] = newNum
			}
		}

		if _, err := tx.ExecContext(ctx, `update fest_teams set `+column+` = null where `+scope+` and deleted = 0`, args...); err != nil {
			return core.FestWrite{}, err
		}
		for teamID, number := range assignments {
			if _, err := tx.ExecContext(ctx, `update fest_teams set `+column+` = ? where id = ? and `+scope+` and deleted = 0`, append([]any{number, teamID}, args...)...); err != nil {
				return core.FestWrite{}, err
			}
		}
		teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
		if err != nil {
			return core.FestWrite{}, err
		}
		if len(entryRemap) == 0 {
			entryRemap = nil
		}
		// Every flat Protocol carries the universal Number, so a reassignment
		// flows into each one's document; KSI's answers follow their team.
		updates, err := roster.PropagateRosterTx(ctx, tx, festID, teams, roster.Remap{Venue: multiFestVenueID, Numbers: entryRemap})
		if err != nil {
			return core.FestWrite{}, err
		}
		written := core.FestWrite{Event: "fest:numbers", Payload: map[string]any{
			"assigned": len(assignments),
			"remapped": len(entryRemap),
		}}
		for _, update := range updates {
			written.Broadcast.States = append(written.Broadcast.States, core.GameState{GameID: update.GameID, StateJSON: update.StateJSON})
		}
		return written, nil
	})
	return err
}

// numbersVenue is the venue a numbers request is about: the one ?venue= names,
// else the first the caller may edit, or 0 in a fest without venues. A venue
// numbers its own teams. The list is the venues the caller may edit.
func (s *Server) numbersVenue(r *http.Request, sc route.Scope) (int64, []multifest.Venue, error) {
	all, err := multifest.List(r.Context(), s.h.Engine().DB, sc.FestID)
	if err != nil || len(all) == 0 {
		return 0, nil, err
	}
	var list []multifest.Venue
	for _, venue := range all {
		if sc.MayEditVenue(venue.ID) {
			list = append(list, venue)
		}
	}
	if len(list) == 0 {
		return 0, nil, route.ErrOtherVenue
	}
	asked, _ := idstr.Parse(r.URL.Query().Get("venue"))
	for _, venue := range list {
		if venue.ID == asked {
			return asked, list, nil
		}
	}
	return list[0].ID, list, nil
}

// VenuePicker switches a page between the venues the caller may edit, and,
// when allLabel is set, all of them together under that label.
func VenuePicker(base string, list []multifest.Venue, current int64, allLabel string) *ui.Element {
	options := make([]ui.Item, 0, len(list)+3)
	options = append(options, ui.Name("venue"), ui.Data("autosubmit", ""))
	if allLabel != "" {
		options = append(options, ui.Option(ui.Value(""), ui.Text(allLabel)))
	}
	for _, venue := range list {
		items := []ui.Item{ui.Value(idstr.Format(venue.ID)), ui.Text(venue.Title)}
		if venue.ID == current {
			items = append(items, ui.Selected())
		}
		options = append(options, ui.Option(items...))
	}
	return ui.Form(ui.Method("get"), ui.Action(base),
		ui.Field(ui.Label(dopestrings.Default.Numbers.Page.Venue()), ui.Selectfield(options...)))
}
