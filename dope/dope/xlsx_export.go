package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// XLSX export of a game's tables for archival. OD games export in the
// rating.chgk.info "tournament-tours" layout (one sheet, per-tour blocks of
// 0/1 cells). KSI and EK export "as they look" — one sheet per in-app view tab,
// pure values, no formulas. Answer cells, which are color-only on screen, are
// rendered as the signed point value the color stands for (+10 / -10 / blank).

// handleScopedGameExport serves GET /api/fest/{fid}/games/{gid}/export.xlsx.
// Gated by read access — anyone who can view the fest can download the archive.
func (s *server) handleScopedGameExport(w http.ResponseWriter, r *http.Request, scope festScope) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeFestRead(w, r, scope.FestID) {
		return
	}

	var gameType, title, schemeJSON, stateJSON string
	err := s.db.QueryRowContext(r.Context(), `
select game_type, title, coalesce(scheme_json, ''), coalesce(state_json, '')
from games where fest_id = ? and id = ?`, scope.FestID, scope.GameID).
		Scan(&gameType, &title, &schemeJSON, &stateJSON)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	f := excelize.NewFile()
	defer f.Close()

	switch gameType {
	case "od":
		err = buildODSheet(f, schemeJSON, stateJSON)
	case "ksi", "si":
		err = buildKSISheets(f, stateJSON)
	case "ek":
		var stages []stageMatches
		if stages, err = s.loadAllStageMatchViews(r.Context(), scope); err == nil {
			err = buildEKSheets(f, stages)
		}
	default:
		http.Error(w, "export not supported for this game type", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// excelize seeds a default "Sheet1"; drop it if our builders added their own.
	if f.SheetCount > 1 {
		_ = f.DeleteSheet("Sheet1")
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", contentDispositionAttachment(exportFileName(title, gameType)))
	if err := f.Write(w); err != nil {
		// Headers may already be flushed; nothing useful to send to the client.
		return
	}
}

// exportFileName derives a download name from the game title, falling back to
// the game type when the title is empty or strips to nothing.
func exportFileName(title, gameType string) string {
	base := sanitizeFileName(title)
	if base == "" {
		base = gameType
	}
	return base + ".xlsx"
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return strings.TrimSpace(replacer.Replace(name))
}

// contentDispositionAttachment builds an RFC 6266 header carrying both an ASCII
// fallback and a UTF-8 (filename*) form, so Cyrillic titles survive the trip.
func contentDispositionAttachment(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", ascii, url.PathEscape(name))
}

// --- OD: rating.chgk.info "tournament-tours" layout ---------------------------

type odExportTeam struct {
	Name   string `json:"name"`
	City   string `json:"city"`
	Number int64  `json:"number"`
}

type odExportState struct {
	Teams     []odExportTeam `json:"teams"`
	Entries   [][]int64      `json:"entries"`
	Completed []bool         `json:"completed"`
}

func buildODSheet(f *excelize.File, schemeJSON, stateJSON string) error {
	tours := parseTourComp(schemeJSON)
	var state odExportState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return fmt.Errorf("parse OD state: %w", err)
		}
	}
	if len(tours) == 0 {
		// No tour composition recorded: fall back to a single tour holding every
		// completed question so the export is never empty.
		tours = []int{len(state.Entries)}
	}

	const sheet = "Worksheet"
	f.SetSheetName("Sheet1", sheet)

	// number → team index, mirroring the live grid's number-keyed scoring.
	indexByNumber := make(map[int64]int, len(state.Teams))
	for i, t := range state.Teams {
		if t.Number != 0 {
			indexByNumber[t.Number] = i
		}
	}
	// took[questionIndex][teamIndex]: a team "took" a completed question when its
	// number appears in that question's entries (questionStats in od.js).
	took := make([]map[int]bool, len(state.Entries))
	for q := range state.Entries {
		if q < len(state.Completed) && !state.Completed[q] {
			continue
		}
		set := make(map[int]bool)
		for _, num := range state.Entries[q] {
			if idx, ok := indexByNumber[num]; ok {
				set[idx] = true
			}
		}
		took[q] = set
	}

	maxTour := 0
	for _, n := range tours {
		if n > maxTour {
			maxTour = n
		}
	}

	row := 2 // reference layout leaves row 1 blank
	qBase := 0
	for tourIdx, tourSize := range tours {
		if tourIdx > 0 {
			row++ // blank separator row between tour blocks
		}
		// Header row, repeated per tour block.
		header := []interface{}{"Team ID", "Название", "Город", "Тур"}
		for i := 1; i <= maxTour; i++ {
			header = append(header, i)
		}
		if err := setRow(f, sheet, row, header); err != nil {
			return err
		}
		row++
		for teamIdx, team := range state.Teams {
			cells := []interface{}{team.Number, team.Name, team.City, tourIdx + 1}
			for i := 0; i < tourSize; i++ {
				q := qBase + i
				v := 0
				if q < len(took) && took[q] != nil && took[q][teamIdx] {
					v = 1
				}
				cells = append(cells, v)
			}
			if err := setRow(f, sheet, row, cells); err != nil {
				return err
			}
			row++
		}
		qBase += tourSize
	}
	return nil
}

// parseTourComp reads scheme.tourComp, which is either a JSON array of ints or a
// comma-separated string with "count*repeat" segments (mirrors od.js).
func parseTourComp(schemeJSON string) []int {
	if schemeJSON == "" {
		return nil
	}
	var probe struct {
		TourComp json.RawMessage `json:"tourComp"`
	}
	if err := json.Unmarshal([]byte(schemeJSON), &probe); err != nil || len(probe.TourComp) == 0 {
		return nil
	}
	var nums []int
	if err := json.Unmarshal(probe.TourComp, &nums); err == nil {
		return filterPositive(nums)
	}
	var s string
	if err := json.Unmarshal(probe.TourComp, &s); err != nil {
		return nil
	}
	var out []int
	for _, seg := range strings.Split(s, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if i := strings.IndexByte(seg, '*'); i >= 0 {
			count, _ := strconv.Atoi(strings.TrimSpace(seg[:i]))
			repeat, _ := strconv.Atoi(strings.TrimSpace(seg[i+1:]))
			for j := 0; j < repeat; j++ {
				if count > 0 {
					out = append(out, count)
				}
			}
			continue
		}
		if n, err := strconv.Atoi(seg); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func filterPositive(in []int) []int {
	out := in[:0:0]
	for _, n := range in {
		if n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// --- KSI: "Подробно" + "Итог" sheets ------------------------------------------

type ksiExportState struct {
	Participants []ksiParticipant `json:"participants"`
	Themes       []struct {
		Answers [][]string `json:"answers"`
	} `json:"themes"`
}

func buildKSISheets(f *excelize.File, stateJSON string) error {
	var state ksiExportState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return fmt.Errorf("parse KSI state: %w", err)
		}
	}
	if err := buildKSIDetailedSheet(f, &state); err != nil {
		return err
	}
	return buildKSIResultsSheet(f, &state)
}

func ksiMark(state *ksiExportState, theme, player, answer int) string {
	if theme >= len(state.Themes) {
		return ""
	}
	answers := state.Themes[theme].Answers
	if player >= len(answers) || answer >= len(answers[player]) {
		return ""
	}
	return answers[player][answer]
}

func buildKSIDetailedSheet(f *excelize.File, state *ksiExportState) error {
	const sheet = "Подробно"
	f.SetSheetName("Sheet1", sheet)
	themesCount := len(state.Themes)
	nv := len(questionValues)

	// Two header rows: theme labels merged across each 6-column block (5 values +
	// score), with the value sub-headers beneath.
	if err := setRow(f, sheet, 1, []interface{}{"Команда", "Σ"}); err != nil {
		return err
	}
	_ = f.MergeCell(sheet, "A1", "A2")
	_ = f.MergeCell(sheet, "B1", "B2")
	col := 3 // first theme block starts at column C
	for t := 0; t < themesCount; t++ {
		left, _ := excelize.CoordinatesToCellName(col, 1)
		right, _ := excelize.CoordinatesToCellName(col+nv, 1)
		_ = f.SetCellValue(sheet, left, fmt.Sprintf("Т%d", t+1))
		_ = f.MergeCell(sheet, left, right)
		sub := make([]interface{}, 0, nv+1)
		for _, v := range questionValues {
			sub = append(sub, v)
		}
		sub = append(sub, "Σ")
		if err := setRowAt(f, sheet, 2, col, sub); err != nil {
			return err
		}
		col += nv + 1
	}

	for p := range state.Participants {
		r := 3 + p
		total := 0
		rowCells := []interface{}{participantExportName(state.Participants, p), nil}
		col := 3
		for t := 0; t < themesCount; t++ {
			themeScore := 0
			for a := 0; a < nv; a++ {
				cell := signedMarkValue(ksiMark(state, t, p, a), questionValues[a])
				if cell != nil {
					themeScore += cell.(int)
				}
				rowCells = append(rowCells, cell)
			}
			total += themeScore
			rowCells = append(rowCells, themeScore)
		}
		_ = col
		rowCells[1] = total
		if err := setRow(f, sheet, r, rowCells); err != nil {
			return err
		}
	}
	return nil
}

func buildKSIResultsSheet(f *excelize.File, state *ksiExportState) error {
	sheet := uniqueSheetName(f, "Итог")
	if _, err := f.NewSheet(sheet); err != nil {
		return err
	}
	// RESULT_VALUES = question values, high to low.
	resultValues := make([]int, len(questionValues))
	for i, v := range questionValues {
		resultValues[len(questionValues)-1-i] = v
	}

	header := []interface{}{"Место", "Команда", "Σ", "Σ+"}
	for _, v := range resultValues {
		header = append(header, v)
	}
	if err := setRow(f, sheet, 1, header); err != nil {
		return err
	}

	type metrics struct {
		index   int
		name    string
		total   int
		plus    int
		correct map[int]int
	}
	rows := make([]metrics, len(state.Participants))
	for p := range state.Participants {
		m := metrics{index: p, name: participantExportName(state.Participants, p), correct: map[int]int{}}
		for t := 0; t < len(state.Themes); t++ {
			for a := 0; a < len(questionValues); a++ {
				v := questionValues[a]
				switch ksiMark(state, t, p, a) {
				case "right":
					m.total += v
					m.plus += v
					m.correct[v]++
				case "wrong":
					m.total -= v
				}
			}
		}
		rows[p] = m
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.total != b.total {
			return a.total > b.total
		}
		if a.plus != b.plus {
			return a.plus > b.plus
		}
		for _, v := range resultValues {
			if a.correct[v] != b.correct[v] {
				return a.correct[v] > b.correct[v]
			}
		}
		return a.index < b.index
	})

	// Tie-grouped place labels ("1", "2–4", ...), matching rankedResultRows.
	sameMetrics := func(a, b metrics) bool {
		if a.total != b.total || a.plus != b.plus {
			return false
		}
		for _, v := range resultValues {
			if a.correct[v] != b.correct[v] {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(rows); {
		j := i
		for j+1 < len(rows) && sameMetrics(rows[i], rows[j+1]) {
			j++
		}
		place := strconv.Itoa(i + 1)
		if j > i {
			place = fmt.Sprintf("%d–%d", i+1, j+1)
		}
		for k := i; k <= j; k++ {
			cells := []interface{}{place, rows[k].name, rows[k].total, rows[k].plus}
			for _, v := range resultValues {
				cells = append(cells, rows[k].correct[v])
			}
			if err := setRow(f, sheet, 2+k, cells); err != nil {
				return err
			}
		}
		i = j + 1
	}
	return nil
}

func participantExportName(participants []ksiParticipant, i int) string {
	if i < len(participants) {
		if n := strings.TrimSpace(participants[i].Name); n != "" {
			return n
		}
	}
	return fmt.Sprintf("Команда %d", i+1)
}

// --- EK: one sheet per stage --------------------------------------------------

func buildEKSheets(f *excelize.File, stages []stageMatches) error {
	if len(stages) == 0 {
		return nil
	}
	for _, stage := range stages {
		name := stage.Code
		if len(stage.Matches) > 0 && strings.TrimSpace(stage.Matches[0].StageTitle) != "" {
			name = stage.Matches[0].StageTitle
		}
		sheet := uniqueSheetName(f, sanitizeSheetName(name))
		if _, err := f.NewSheet(sheet); err != nil {
			return err
		}
		row := 1
		for _, mv := range stage.Matches {
			n, err := writeEKMatchBlock(f, sheet, row, mv)
			if err != nil {
				return err
			}
			row += n + 1 // blank row between match tables
		}
	}
	return nil
}

// writeEKMatchBlock writes one match's flat score table starting at startRow and
// returns the number of rows written. Mirrors the on-screen match-table:
// Команда | Σ | М | per-theme(values + score) | shootout themes | R.
func writeEKMatchBlock(f *excelize.File, sheet string, startRow int, mv MatchView) (int, error) {
	nv := len(mv.QuestionValues)

	maxThemes, maxShootout := 0, 0
	for _, t := range mv.Teams {
		if len(t.Themes) > maxThemes {
			maxThemes = len(t.Themes)
		}
		if len(t.ShootoutThemes) > maxShootout {
			maxShootout = len(t.ShootoutThemes)
		}
	}

	row := startRow
	// Match title row.
	title := strings.TrimSpace(mv.Title)
	if title == "" {
		title = mv.Code
	}
	if err := setRow(f, sheet, row, []interface{}{title}); err != nil {
		return 0, err
	}
	row++

	// Theme label header row.
	labels := []interface{}{"Команда", "Σ", "М"}
	for t := 0; t < maxThemes; t++ {
		labels = append(labels, fmt.Sprintf("Т%d", t+1))
		for i := 1; i < nv+1; i++ {
			labels = append(labels, nil)
		}
	}
	for t := 0; t < maxShootout; t++ {
		labels = append(labels, fmt.Sprintf("П%d", t+1))
		for i := 1; i < nv+1; i++ {
			labels = append(labels, nil)
		}
	}
	if maxShootout > 0 || maxThemes > 0 {
		labels = append(labels, "R")
	}
	if err := setRow(f, sheet, row, labels); err != nil {
		return 0, err
	}
	row++

	// Value sub-header row.
	sub := []interface{}{nil, nil, nil}
	appendValueBlock := func(n int) {
		for t := 0; t < n; t++ {
			for _, v := range mv.QuestionValues {
				sub = append(sub, v)
			}
			sub = append(sub, "Σ")
		}
	}
	appendValueBlock(maxThemes)
	appendValueBlock(maxShootout)
	if maxShootout > 0 || maxThemes > 0 {
		sub = append(sub, nil)
	}
	if err := setRow(f, sheet, row, sub); err != nil {
		return 0, err
	}
	row++

	for _, team := range mv.Teams {
		place := interface{}(nil)
		if team.Place > 0 {
			place = formatPlace(team.Place)
		}
		cells := []interface{}{team.Name, team.Total, place}
		writeThemes := func(themes []ThemeView, count int) {
			for t := 0; t < count; t++ {
				var tv *ThemeView
				if t < len(themes) {
					tv = &themes[t]
				}
				for a := 0; a < nv; a++ {
					mark := ""
					if tv != nil && a < len(tv.Answers) {
						mark = tv.Answers[a]
					}
					cells = append(cells, signedMarkValue(mark, mv.QuestionValues[a]))
				}
				if tv != nil {
					cells = append(cells, tv.Score)
				} else {
					cells = append(cells, nil)
				}
			}
		}
		writeThemes(team.Themes, maxThemes)
		writeThemes(team.ShootoutThemes, maxShootout)
		if maxShootout > 0 || maxThemes > 0 {
			cells = append(cells, team.Tiebreak)
		}
		if err := setRow(f, sheet, row, cells); err != nil {
			return 0, err
		}
		row++
	}
	return row - startRow, nil
}

func formatPlace(p float64) interface{} {
	if p == float64(int64(p)) {
		return int64(p)
	}
	return p
}

// --- shared helpers -----------------------------------------------------------

// signedMarkValue maps an answer mark to the signed point value its cell color
// stands for on screen: right → +value, wrong → -value, anything else → blank.
func signedMarkValue(mark string, value int) interface{} {
	switch mark {
	case "right":
		return value
	case "wrong":
		return -value
	default:
		return nil
	}
}

func setRow(f *excelize.File, sheet string, row int, values []interface{}) error {
	return setRowAt(f, sheet, row, 1, values)
}

func setRowAt(f *excelize.File, sheet string, row, startCol int, values []interface{}) error {
	for i, v := range values {
		if v == nil {
			continue
		}
		cell, err := excelize.CoordinatesToCellName(startCol+i, row)
		if err != nil {
			return err
		}
		if err := f.SetCellValue(sheet, cell, v); err != nil {
			return err
		}
	}
	return nil
}

// sanitizeSheetName strips characters Excel forbids in sheet names and clamps to
// the 31-char limit.
func sanitizeSheetName(name string) string {
	name = strings.TrimSpace(name)
	replacer := strings.NewReplacer("/", "-", "\\", "-", "?", "", "*", "", "[", "(", "]", ")", ":", "-")
	name = strings.TrimSpace(replacer.Replace(name))
	if name == "" {
		name = "Лист"
	}
	if r := []rune(name); len(r) > 31 {
		name = string(r[:31])
	}
	return name
}

// uniqueSheetName disambiguates collisions by appending " (n)" within the limit.
func uniqueSheetName(f *excelize.File, base string) string {
	base = sanitizeSheetName(base)
	existing := make(map[string]bool)
	for _, n := range f.GetSheetList() {
		existing[n] = true
	}
	if !existing[base] {
		return base
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprintf(" (%d)", i)
		trimmed := base
		if r := []rune(base); len(r)+len(suffix) > 31 {
			trimmed = string(r[:31-len(suffix)])
		}
		candidate := trimmed + suffix
		if !existing[candidate] {
			return candidate
		}
	}
}
