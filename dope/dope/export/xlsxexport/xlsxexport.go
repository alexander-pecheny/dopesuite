// Package xlsxexport renders a game's tables to an .xlsx workbook for archival.
// OD games export in the rating.chgk.info "tournament-tours" layout (one sheet,
// per-tour blocks of 0/1 cells). KSI and EK export "as they look" — one sheet
// per in-app view tab, pure values, no formulas. Answer cells, which are
// colour-only on screen, are rendered as the signed point value the colour
// stands for (+10 / -10 / blank).
//
// It is a leaf over the domain packages: it imports games (OD/KSI domain) and
// store (view types, scoring scale) plus excelize, and never the server. The
// HTTP handler that loads the data and streams the file stays in package main.
package xlsxexport

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"dope/dope/domain/games"
	"dope/dope/storage/store"
)

// --- OD: rating.chgk.info "tournament-tours" layout ---------------------------

// BuildODSheet writes the OD "tournament-tours" worksheet. ratingByNumber maps a
// team number to its rating.chgk.info id (used as the Team ID when present).
func BuildODSheet(f *excelize.File, schemeJSON, stateJSON string, ratingByNumber map[int64]int64) error {
	tours := games.ParseTourComp(schemeJSON)
	var state games.ODState
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
			teamID := interface{}(team.Number)
			if rid, ok := ratingByNumber[team.Number]; ok {
				teamID = rid
			}
			cells := []interface{}{teamID, team.Name, team.City, tourIdx + 1}
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

// --- KSI: "Подробно" + "Итог" sheets ------------------------------------------

type ksiExportState struct {
	Participants []games.KSIParticipant `json:"participants"`
	Declined     map[string]bool        `json:"declined"`
	Stickers     [][]string             `json:"stickers"`
	Themes       []struct {
		Answers [][]string `json:"answers"`
	} `json:"themes"`
	// stickersMode is derived from the scheme: when true, a theme's value is
	// scored under the per-(team,theme) sticker and is excluded entirely until a
	// sticker is chosen.
	stickersMode bool
}

// BuildKSISheets writes the KSI "Подробно" (detailed) and "Итог" (results)
// sheets. schemeJSON is consulted only to detect the "KSI with stickers"
// variant (its `stickers` config); plain KSI games score exactly as before.
func BuildKSISheets(f *excelize.File, schemeJSON, stateJSON string) error {
	var state ksiExportState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return fmt.Errorf("parse KSI state: %w", err)
		}
	}
	state.stickersMode = ksiSchemeHasStickers(schemeJSON)
	if err := buildKSIDetailedSheet(f, &state); err != nil {
		return err
	}
	return buildKSIResultsSheet(f, &state, schemeJSON, stateJSON)
}

func ksiSchemeHasStickers(schemeJSON string) bool {
	if strings.TrimSpace(schemeJSON) == "" {
		return false
	}
	var scheme struct {
		Stickers games.KSIStickerConfig `json:"stickers"`
	}
	if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
		return false
	}
	return len(scheme.Stickers.Types) > 0
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

// ksiThemeSticker returns the sticker scoring a (theme, player) and whether the
// theme is scored at all. Plain KSI games always score every theme under the
// neutral rules; stickers games leave a theme unscored until its sticker is set.
func ksiThemeSticker(state *ksiExportState, theme, player int) (string, bool) {
	if !state.stickersMode {
		return games.KSIStickerNeutral, true
	}
	if theme < len(state.Stickers) && player < len(state.Stickers[theme]) {
		if id := state.Stickers[theme][player]; id != "" {
			return id, true
		}
	}
	return "", false
}

func buildKSIDetailedSheet(f *excelize.File, state *ksiExportState) error {
	const sheet = "Подробно"
	f.SetSheetName("Sheet1", sheet)
	themesCount := len(state.Themes)
	nv := len(store.QuestionValues)

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
		for _, v := range store.QuestionValues {
			sub = append(sub, v)
		}
		sub = append(sub, "Σ")
		if err := setRowAt(f, sheet, 2, col, sub); err != nil {
			return err
		}
		col += nv + 1
	}

	r := 3 // output row; advances only for teams that played (declined teams omitted)
	for p := range state.Participants {
		if games.KSIParticipantDeclined(state.Declined, state.Participants[p]) {
			continue
		}
		total := 0
		rowCells := []interface{}{participantExportName(state.Participants, p), nil}
		for t := 0; t < themesCount; t++ {
			sticker, scored := ksiThemeSticker(state, t, p)
			themeScore := 0
			for a := 0; a < nv; a++ {
				var cell interface{}
				if scored {
					mark := ksiMark(state, t, p, a)
					cv := games.KSIStickerMarkValue(sticker, mark, store.QuestionValues[a])
					if mark == "right" || mark == "wrong" || cv != 0 {
						cell = cv
						themeScore += cv
					}
				}
				rowCells = append(rowCells, cell)
			}
			if scored {
				total += themeScore
				rowCells = append(rowCells, themeScore)
			} else {
				// Unscored theme (stickers game, no sticker yet): leave Σ blank.
				rowCells = append(rowCells, nil)
			}
		}
		rowCells[1] = total
		if err := setRow(f, sheet, r, rowCells); err != nil {
			return err
		}
		r++
	}
	return nil
}

func buildKSIResultsSheet(f *excelize.File, state *ksiExportState, schemeJSON, stateJSON string) error {
	sheet := uniqueSheetName(f, "Итог")
	if _, err := f.NewSheet(sheet); err != nil {
		return err
	}
	// RESULT_VALUES = question values, high to low.
	resultValues := make([]int, len(store.QuestionValues))
	for i, v := range store.QuestionValues {
		resultValues[len(store.QuestionValues)-1-i] = v
	}

	header := []interface{}{"Место", "Команда", "Σ", "Σ+"}
	for _, v := range resultValues {
		header = append(header, v)
	}
	if err := setRow(f, sheet, 1, header); err != nil {
		return err
	}

	ranked, err := games.ComputeKSIResults(schemeJSON, stateJSON, store.QuestionValues[:])
	if err != nil {
		return err
	}
	// Tie-grouped place labels ("1", "2–4", ...), matching rankedResultRows.
	for i := 0; i < len(ranked); {
		j := i
		for j+1 < len(ranked) && ranked[j+1].Place == ranked[i].Place {
			j++
		}
		place := strconv.Itoa(i + 1)
		if j > i {
			place = fmt.Sprintf("%d–%d", i+1, j+1)
		}
		for k := i; k <= j; k++ {
			cells := []interface{}{place, participantExportName(state.Participants, ranked[k].Index), ranked[k].Total, ranked[k].Plus}
			for _, v := range resultValues {
				cells = append(cells, ranked[k].Correct[v])
			}
			if err := setRow(f, sheet, 2+k, cells); err != nil {
				return err
			}
		}
		i = j + 1
	}
	return nil
}

func participantExportName(participants []games.KSIParticipant, i int) string {
	if i < len(participants) {
		if n := strings.TrimSpace(participants[i].Name); n != "" {
			return n
		}
	}
	return fmt.Sprintf("Команда %d", i+1)
}

// --- EK: one sheet per stage --------------------------------------------------

// BuildEKSheets writes the EK per-stage match sheets plus the leading
// "Статистика" per-player overview.
func BuildEKSheets(f *excelize.File, stages []store.StageMatches) error {
	if len(stages) == 0 {
		return nil
	}
	// "Статистика" first (leftmost) so the per-player overview — the most readable
	// view — opens by default. Skipped when no answers are marked anywhere.
	if err := buildEKStatsSheet(f, stages); err != nil {
		return err
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
		_ = f.SetColWidth(sheet, "A", "A", 26) // team / player name column
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

// axis converts 1-based (col, row) to an A1 cell reference for merges.
func axis(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}

// writeEKMatchBlock writes one match's score table starting at startRow and
// returns the number of rows written. It mirrors the on-screen two-row match
// layout: per team a "names" row (team, Σ, place, the player who played each
// theme, theme score, R) and an "answers" row beneath it carrying the signed
// per-question marks under each player. The player name is merged across its
// theme's value columns so it sits above that player's answers, exactly as the
// page renders it.
func writeEKMatchBlock(f *excelize.File, sheet string, startRow int, mv store.MatchView) (int, error) {
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
	blockCount := maxThemes + maxShootout
	hasR := blockCount > 0
	// Each theme block is nv value columns + 1 score column; blocks start at col 4.
	blockStart := func(b int) int { return 4 + b*(nv+1) }

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

	// Theme label header row: "Т1".."Тn" then "П1".. ; each label spans its value
	// columns, with a "Σ" score-column header after it.
	labelRow := row
	labels := []interface{}{"Команда", "Σ", "М"}
	for t := 0; t < maxThemes; t++ {
		labels = append(labels, fmt.Sprintf("Т%d", t+1))
		for i := 1; i < nv; i++ {
			labels = append(labels, nil)
		}
		labels = append(labels, "Σ")
	}
	for t := 0; t < maxShootout; t++ {
		labels = append(labels, fmt.Sprintf("П%d", t+1))
		for i := 1; i < nv; i++ {
			labels = append(labels, nil)
		}
		labels = append(labels, "Σ")
	}
	if hasR {
		labels = append(labels, "R")
	}
	if err := setRow(f, sheet, labelRow, labels); err != nil {
		return 0, err
	}
	row++

	// Value sub-header row: the nominal point values under each theme block.
	sub := []interface{}{nil, nil, nil}
	for b := 0; b < blockCount; b++ {
		for _, v := range mv.QuestionValues {
			sub = append(sub, v)
		}
		sub = append(sub, nil) // score column
	}
	if hasR {
		sub = append(sub, nil)
	}
	if err := setRow(f, sheet, row, sub); err != nil {
		return 0, err
	}
	row++

	if nv > 1 {
		for b := 0; b < blockCount; b++ {
			_ = f.MergeCell(sheet, axis(blockStart(b), labelRow), axis(blockStart(b)+nv-1, labelRow))
		}
	}

	for _, team := range mv.Teams {
		place := interface{}(nil)
		if team.Place > 0 {
			place = formatPlace(team.Place)
		}
		nameRow := row
		ansRow := row + 1
		nameCells := []interface{}{team.Name, team.Total, place}
		ansCells := []interface{}{nil, nil, nil}
		appendBlocks := func(themes []store.ThemeView, count int) {
			for t := 0; t < count; t++ {
				var tv *store.ThemeView
				if t < len(themes) {
					tv = &themes[t]
				}
				player := ""
				if tv != nil {
					player = tv.Player
				}
				nameCells = append(nameCells, player)
				for i := 1; i < nv; i++ {
					nameCells = append(nameCells, nil)
				}
				if tv != nil {
					nameCells = append(nameCells, tv.Score)
				} else {
					nameCells = append(nameCells, nil)
				}
				for a := 0; a < nv; a++ {
					mark := ""
					if tv != nil && a < len(tv.Answers) {
						mark = tv.Answers[a]
					}
					ansCells = append(ansCells, signedMarkValue(store.NormalizeMark(mark), mv.QuestionValues[a]))
				}
				ansCells = append(ansCells, nil) // score column
			}
		}
		appendBlocks(team.Themes, maxThemes)
		appendBlocks(team.ShootoutThemes, maxShootout)
		if hasR {
			nameCells = append(nameCells, team.Tiebreak)
			ansCells = append(ansCells, nil)
		}
		if err := setRow(f, sheet, nameRow, nameCells); err != nil {
			return 0, err
		}
		if err := setRow(f, sheet, ansRow, ansCells); err != nil {
			return 0, err
		}
		if nv > 1 {
			for b := 0; b < blockCount; b++ {
				_ = f.MergeCell(sheet, axis(blockStart(b), nameRow), axis(blockStart(b)+nv-1, nameRow))
			}
		}
		row += 2
	}
	return row - startRow, nil
}

// ekPlayerStat is one row of the "Статистика" sheet: a player's aggregate across
// every match they played, mirroring computeEKPlayerStats in static/match-table.js.
type ekPlayerStat struct {
	Player  string
	Team    string
	Sum     int     // Σ: signed point total
	Plus    int     // Σ+: points from correct answers only
	Battles int     // Бои: distinct matches the player appeared in
	Right   [5]int  // correct counts by value index (0→10 … 4→50)
	Wrong   [5]int  // wrong counts by value index
	Share   float64 // 0..1 share among the team's positive contributors
}

// computeEKPlayerStats aggregates per-player stats across all stages/matches,
// keyed by (team, player), mirroring the client-side helper of the same name.
// Only regular themes count (not shootout), matching the on-screen table.
func computeEKPlayerStats(stages []store.StageMatches) []ekPlayerStat {
	values := [5]int{10, 20, 30, 40, 50}
	type agg struct {
		stat    ekPlayerStat
		battles map[string]bool
	}
	byKey := map[string]*agg{}
	order := []string{}
	for _, stage := range stages {
		for _, match := range stage.Matches {
			battleID := stage.Code + match.Code
			for _, team := range match.Teams {
				for _, theme := range team.Themes {
					player := strings.TrimSpace(theme.Player)
					if player == "" {
						continue
					}
					key := team.Name + "\x00" + player
					a := byKey[key]
					if a == nil {
						a = &agg{stat: ekPlayerStat{Player: player, Team: team.Name}, battles: map[string]bool{}}
						byKey[key] = a
						order = append(order, key)
					}
					if !a.battles[battleID] {
						a.battles[battleID] = true
						a.stat.Battles++
					}
					for i, mark := range theme.Answers {
						v := 0
						if i < len(values) {
							v = values[i]
						}
						switch store.NormalizeMark(mark) {
						case "right":
							a.stat.Sum += v
							a.stat.Plus += v
							a.stat.Right[i]++
						case "wrong":
							a.stat.Sum -= v
							a.stat.Wrong[i]++
						}
					}
				}
			}
		}
	}
	// "% от команды": a positive player's share among their team's positive
	// contributors, so a team's positive players' shares sum to 100%.
	teamPositive := map[string]int{}
	rows := make([]ekPlayerStat, 0, len(order))
	for _, key := range order {
		st := byKey[key].stat
		if st.Sum > 0 {
			teamPositive[st.Team] += st.Sum
		}
		rows = append(rows, st)
	}
	for i := range rows {
		if total := teamPositive[rows[i].Team]; rows[i].Sum > 0 && total > 0 {
			rows[i].Share = float64(rows[i].Sum) / float64(total)
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		if rows[a].Sum != rows[b].Sum {
			return rows[a].Sum > rows[b].Sum
		}
		if rows[a].Plus != rows[b].Plus {
			return rows[a].Plus > rows[b].Plus
		}
		return rows[a].Player < rows[b].Player
	})
	return rows
}

// buildEKStatsSheet writes the per-player "Статистика" sheet. Columns mirror the
// on-screen table: Игрок, Команда, Σ, Σ+, Бои, 50…10 (correct counts), −50…−10
// (wrong counts), % от команды. No sheet is added when nothing is scored yet.
func buildEKStatsSheet(f *excelize.File, stages []store.StageMatches) error {
	rows := computeEKPlayerStats(stages)
	if len(rows) == 0 {
		return nil
	}
	sheet := uniqueSheetName(f, "Статистика")
	if _, err := f.NewSheet(sheet); err != nil {
		return err
	}
	header := []interface{}{"Игрок", "Команда", "Σ", "Σ+", "Бои"}
	for _, v := range []int{50, 40, 30, 20, 10} {
		header = append(header, v)
	}
	for _, v := range []int{50, 40, 30, 20, 10} {
		header = append(header, fmt.Sprintf("-%d", v))
	}
	header = append(header, "% от команды")
	if err := setRow(f, sheet, 1, header); err != nil {
		return err
	}
	for i, r := range rows {
		cells := []interface{}{r.Player, r.Team, r.Sum, r.Plus, r.Battles}
		for v := 4; v >= 0; v-- {
			cells = append(cells, r.Right[v])
		}
		for v := 4; v >= 0; v-- {
			cells = append(cells, r.Wrong[v])
		}
		cells = append(cells, fmt.Sprintf("%d%%", int(r.Share*100+0.5)))
		if err := setRow(f, sheet, i+2, cells); err != nil {
			return err
		}
	}
	_ = f.SetColWidth(sheet, "A", "B", 24) // player + team names
	_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	return nil
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
