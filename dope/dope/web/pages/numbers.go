package pages

import (
	"context"
	"database/sql"
	"dope/dope/domain/numbering"
	"dope/dope/domain/roster"
	"dope/dope/domain/view"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	ui "dope/dope/web/ui"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type hostFestNumberRow struct {
	Index     int
	Number    string
	TeamID    int64
	TeamLabel string
}

type hostFestNumbersData struct {
	Fest       view.HostFest
	Rows       []hostFestNumberRow
	Error      string
	Notice     string
	HasNumbers bool
}

// hostNumbersDoc builds the fest team-numbers editor: the read-only number list
// with the actions cluster (clear/auto/import/edit). The edit-in-place toggle and
// the two mass-import <dialog> modals are driven by numbers.js (keyed on the ids
// and data-has-numbers here); the DSL page carries no script.
func hostNumbersDoc(data hostFestNumbersData) *ui.Doc {
	ref := data.Fest.Ref()
	page := []ui.Item{
		ui.Title(data.Fest.Title + " · номера команд"), ui.PagePublic, ui.Classicscripts("dist/numbers.js"),
		ui.Publictopbar(ui.Title("Номера команд"), ui.Back("/host/fest/"+ref)),
	}
	if data.Error != "" {
		page = append(page, ui.Empty(ui.Text(data.Error)))
	}
	if data.Notice != "" {
		page = append(page, ui.Note(ui.Text(data.Notice)))
	}
	if len(data.Rows) == 0 {
		page = append(page, ui.Empty(ui.Text("Сначала загрузите команды на странице феста.")))
		return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
	}

	hasNum := ""
	if data.HasNumbers {
		hasNum = "1"
	}
	base := "/host/fest/" + ref + "/numbers"

	actions := []ui.Item{ui.Wrap()}
	if data.HasNumbers {
		actions = append(actions, ui.Button(ui.Submit(), ui.ID("numbers-clear-btn"),
			ui.Formaction(base+"/clear"), ui.Formnovalidate(), ui.Text("Очистить")))
	}
	actions = append(actions,
		ui.Button(ui.Submit(), ui.ID("numbers-auto-btn"), ui.Formaction(base+"/auto"), ui.Formnovalidate(), ui.Text("Проставить автоматически")),
		ui.Button(ui.ID("numbers-import-btn"), ui.Text("Импорт номеров")),
	)
	if data.HasNumbers {
		actions = append(actions, ui.Button(ui.ID("numbers-edit-btn"), ui.Text("Замена номера")))
	}

	rows := make([]ui.Item, 0, len(data.Rows))
	for _, row := range data.Rows {
		teamID := ""
		if row.TeamID != 0 {
			teamID = strconv.FormatInt(row.TeamID, 10)
		}
		rows = append(rows, ui.Numberrow(
			ui.Index(strconv.Itoa(row.Index)), ui.Num(row.Number),
			ui.Teamlabel(row.TeamLabel), ui.Teamid(teamID),
		))
	}

	form := []ui.Item{ui.ID("numbers-form"), ui.Action(base), ui.Data("has-numbers", hasNum)}
	form = append(form,
		ui.Row(actions...),
		ui.Note(ui.ID("numbers-help"), ui.Hidden(), ui.Text(
			"Меняйте номер прямо в строке. Когда сохраните, все упоминания старого номера в ОД заменятся на новый — удобно, чтобы перевести команду на резервный номер (101 и т. п.), не задевая остальных.")),
		ui.Numberlist(rows...),
		ui.Row(ui.ID("numbers-save"), ui.Hidden(),
			ui.Button(ui.Submit(), ui.Text("Сохранить")),
			ui.Button(ui.Secondary, ui.ID("numbers-cancel-btn"), ui.Text("Отмена")),
		),
	)
	page = append(page, ui.Numbersform(form...))
	return &ui.Doc{Nodes: []ui.Node{ui.Page(page...)}}
}

func (s *Server) RenderHostFestNumbers(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string, override []hostFestNumberRow) {
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := s.buildHostFestNumbersData(r.Context(), festID, errMsg, notice, override)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Fest = fest
	RenderDoc(w, s.h.Engine().AssetETags, hostNumbersDoc(data))
}

func (s *Server) buildHostFestNumbersData(ctx context.Context, festID int64, errMsg, notice string, override []hostFestNumberRow) (hostFestNumbersData, error) {
	teams, err := numbering.LoadFestTeams(ctx, s.h.DB(), festID)
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
			if v, err := strconv.ParseInt(idRaw, 10, 64); err == nil && v > 0 {
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

func (s *Server) HandleHostSaveFestNumbers(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	teams, err := numbering.LoadFestTeams(r.Context(), s.h.DB(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(teams) == 0 {
		s.RenderHostFestNumbers(w, r, festID, "Сначала загрузите команды.", "", nil)
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
				s.RenderHostFestNumbers(w, r, festID, fmt.Sprintf("Строка %d: укажите команду или удалите номер.", row.Index), "", override)
				return
			}
			continue
		}
		if !validIDs[row.TeamID] {
			s.RenderHostFestNumbers(w, r, festID, fmt.Sprintf("Строка %d: команда не из этого феста.", row.Index), "", override)
			return
		}
		if prev, ok := teamToRow[row.TeamID]; ok {
			s.RenderHostFestNumbers(w, r, festID, fmt.Sprintf("Команда выбрана сразу в строках %d и %d.", prev, row.Index), "", override)
			return
		}
		teamToRow[row.TeamID] = row.Index
		if !hasNum {
			// Team intentionally left without a number — keep it unnumbered.
			continue
		}
		n, err := strconv.Atoi(numTxt)
		if err != nil || n <= 0 || n > numbering.MaxNumber {
			s.RenderHostFestNumbers(w, r, festID, fmt.Sprintf("Строка %d: номер должен быть целым от 1 до %d.", row.Index, numbering.MaxNumber), "", override)
			return
		}
		if prev, ok := numberToRow[n]; ok {
			s.RenderHostFestNumbers(w, r, festID, fmt.Sprintf("Номер %d указан сразу в строках %d и %d.", n, prev, row.Index), "", override)
			return
		}
		numberToRow[n] = row.Index
		assignments[row.TeamID] = n
	}

	if err := s.SaveFestNumbers(r.Context(), festID, assignments); err != nil {
		s.RenderHostFestNumbers(w, r, festID, err.Error(), "", override)
		return
	}
	notice := "Номера сохранены."
	if len(assignments) < len(teams) {
		notice = fmt.Sprintf("Сохранено. Осталось без номера: %d.", len(teams)-len(assignments))
	}
	s.RenderHostFestNumbers(w, r, festID, "", notice, nil)
}

func (s *Server) HandleHostAutoFestNumbers(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := s.purgeFestSoftDeletedTeams(r.Context(), festID); err != nil {
		s.RenderHostFestNumbers(w, r, festID, err.Error(), "", nil)
		return
	}
	teams, err := numbering.LoadFestTeams(r.Context(), s.h.DB(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(teams) == 0 {
		s.RenderHostFestNumbers(w, r, festID, "Сначала загрузите команды.", "", nil)
		return
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
	if err := s.SaveFestNumbers(r.Context(), festID, assignments); err != nil {
		s.RenderHostFestNumbers(w, r, festID, err.Error(), "", nil)
		return
	}
	s.RenderHostFestNumbers(w, r, festID, "", "Номера проставлены автоматически по алфавиту.", nil)
}

func (s *Server) HandleHostClearFestNumbers(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := s.purgeFestSoftDeletedTeams(r.Context(), festID); err != nil {
		s.RenderHostFestNumbers(w, r, festID, err.Error(), "", nil)
		return
	}
	if err := s.SaveFestNumbers(r.Context(), festID, nil); err != nil {
		s.RenderHostFestNumbers(w, r, festID, err.Error(), "", nil)
		return
	}
	s.RenderHostFestNumbers(w, r, festID, "", "Номера очищены.", nil)
}

// purgeFestSoftDeletedTeams hard-deletes any soft-deleted fest_teams rows for
// this fest. Used by the "assign numbers" and "clear" actions, which the host
// has explicitly confirmed as destructive resets — archived numbers from teams
// that left the roster must not block reuse of those numbers.
func (s *Server) purgeFestSoftDeletedTeams(reqCtx context.Context, festID int64) error {
	return s.h.WithWriteTx(reqCtx, festID, "purge-soft-deleted-teams", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from fest_teams where fest_id = ? and deleted = 1`, festID)
		return err
	})
}

func (s *Server) SaveFestNumbers(reqCtx context.Context, festID int64, assignments map[int64]int) error {
	var updates []roster.GameStateBroadcast
	var revision int64
	err := s.h.WithWriteTx(reqCtx, festID, "fest-numbers", func(ctx context.Context, tx *sql.Tx) error {
		oldTeams, err := numbering.LoadFestTeams(ctx, tx, festID)
		if err != nil {
			return err
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

		if _, err := tx.ExecContext(ctx, `update fest_teams set number = null where fest_id = ? and deleted = 0`, festID); err != nil {
			return err
		}
		for teamID, number := range assignments {
			if _, err := tx.ExecContext(ctx, `update fest_teams set number = ? where id = ? and fest_id = ? and deleted = 0`, number, teamID, festID); err != nil {
				return err
			}
		}
		teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
		if err != nil {
			return err
		}
		if len(entryRemap) == 0 {
			entryRemap = nil
		}
		chgkUpdates, err := roster.PropagateRosterToChGKTx(ctx, tx, festID, teams, entryRemap)
		if err != nil {
			return err
		}
		// KSI carries the same universal team number in its participants list, so a
		// number reassignment must flow into KSI states too (the full roster import
		// propagates to both; the number-edit path used to skip KSI, leaving its
		// numbers stale relative to OD). Answers follow each team by name when the
		// number changed, so scores are preserved.
		ksiUpdates, err := roster.PropagateRosterToKSITx(ctx, tx, festID, teams)
		if err != nil {
			return err
		}
		updates = append(chgkUpdates, ksiUpdates...)
		revision, err = festwrite.BumpFestRevisionTx(ctx, tx, festID, "fest:numbers", util.MustJSON(map[string]any{
			"assigned": len(assignments),
			"remapped": len(entryRemap),
		}))
		return err
	})
	if err != nil {
		return err
	}
	for _, update := range updates {
		s.h.BroadcastState(festID, fmt.Sprintf("game-state:%d", update.GameID), revision, update.StateJSON)
	}
	return nil
}
