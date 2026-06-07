package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func cell(t *testing.T, f *excelize.File, sheet, axis string) string {
	t.Helper()
	v, err := f.GetCellValue(sheet, axis)
	if err != nil {
		t.Fatalf("GetCellValue %s!%s: %v", sheet, axis, err)
	}
	return v
}

func TestBuildODSheet(t *testing.T) {
	scheme := `{"gameType":"od","tourComp":[3,2]}`
	// Two teams (numbers 1, 2). Tour 1 = questions 0..2, tour 2 = questions 3..4.
	// Team 1 took q0,q1,q3; team 2 took q2. q4 not completed → nobody took it.
	state := `{
		"teams":[{"name":"Альфа","city":"Минск","number":1},{"name":"Бета","city":"Киев","number":2}],
		"entries":[[1],[1],[2],[1],[2]],
		"completed":[true,true,true,true,false]
	}`
	f := excelize.NewFile()
	defer f.Close()
	if err := buildODSheet(f, scheme, state); err != nil {
		t.Fatalf("buildODSheet: %v", err)
	}
	const sheet = "Worksheet"
	if got := f.GetSheetList(); len(got) != 1 || got[0] != sheet {
		t.Fatalf("sheets = %v, want [%s]", got, sheet)
	}
	// Header row at row 2, max tour width = 3 questions.
	if got := cell(t, f, sheet, "A2"); got != "Team ID" {
		t.Fatalf("A2 = %q, want Team ID", got)
	}
	if got := cell(t, f, sheet, "D2"); got != "Тур" {
		t.Fatalf("D2 = %q, want Тур", got)
	}
	if got := cell(t, f, sheet, "G2"); got != "3" {
		t.Fatalf("G2 (last question header) = %q, want 3", got)
	}
	// Tour 1, team Альфа (row 3): number, name, city, tour, then 1,1,0.
	if got := cell(t, f, sheet, "A3"); got != "1" {
		t.Fatalf("A3 team id = %q, want 1", got)
	}
	if got := cell(t, f, sheet, "B3"); got != "Альфа" {
		t.Fatalf("B3 name = %q", got)
	}
	if got := cell(t, f, sheet, "D3"); got != "1" {
		t.Fatalf("D3 tour = %q, want 1", got)
	}
	if got := cell(t, f, sheet, "E3"); got != "1" { // q0 taken
		t.Fatalf("E3 = %q, want 1", got)
	}
	if got := cell(t, f, sheet, "G3"); got != "0" { // q2 taken by team 2, not team1
		t.Fatalf("G3 = %q, want 0", got)
	}
	// Tour 1 has 2 teams (rows 3-4), then a blank separator (row 5), the repeated
	// header (row 6), and tour 2's data (rows 7-8).
	if got := cell(t, f, sheet, "A5"); got != "" {
		t.Fatalf("A5 separator = %q, want empty", got)
	}
	if got := cell(t, f, sheet, "A6"); got != "Team ID" {
		t.Fatalf("A6 repeated header = %q", got)
	}
	if got := cell(t, f, sheet, "D7"); got != "2" {
		t.Fatalf("D7 tour = %q, want 2", got)
	}
	// Tour 2: team Альфа took q3 (col E), q4 not completed (col F = 0).
	if got := cell(t, f, sheet, "E7"); got != "1" {
		t.Fatalf("E7 = %q, want 1", got)
	}
	if got := cell(t, f, sheet, "F7"); got != "0" {
		t.Fatalf("F7 (uncompleted q4) = %q, want 0", got)
	}
}

func TestBuildKSISheets(t *testing.T) {
	// Two participants, two themes, values [10,20,30,40,50].
	// P0 (Гамма): theme0 right on 10 and 30, wrong on 20 → score 10-20+30=20.
	// P1 (Дельта): theme0 right on 50 → score 50.
	state := `{
		"participants":[{"number":7,"name":"Гамма"},{"number":8,"name":"Дельта"}],
		"themes":[
			{"answers":[["right","wrong","right","",""],["","","","","right"]]},
			{"answers":[["","","","",""],["","","","",""]]}
		]
	}`
	f := excelize.NewFile()
	defer f.Close()
	if err := buildKSISheets(f, state); err != nil {
		t.Fatalf("buildKSISheets: %v", err)
	}
	sheets := f.GetSheetList()
	wantSheets := map[string]bool{"Подробно": false, "Итог": false}
	for _, s := range sheets {
		if _, ok := wantSheets[s]; ok {
			wantSheets[s] = true
		}
	}
	for name, found := range wantSheets {
		if !found {
			t.Fatalf("missing sheet %q (have %v)", name, sheets)
		}
	}

	// Подробно: header, value sub-headers, signed answer values + theme score.
	const det = "Подробно"
	if got := cell(t, f, det, "A1"); got != "Команда" {
		t.Fatalf("det A1 = %q", got)
	}
	if got := cell(t, f, det, "C1"); got != "Т1" {
		t.Fatalf("det C1 theme label = %q", got)
	}
	if got := cell(t, f, det, "C2"); got != "10" {
		t.Fatalf("det C2 value header = %q", got)
	}
	// Гамма is row 3: name, total(20), then theme1 cells C..H.
	if got := cell(t, f, det, "A3"); got != "Гамма" {
		t.Fatalf("det A3 = %q", got)
	}
	if got := cell(t, f, det, "B3"); got != "20" {
		t.Fatalf("det B3 total = %q, want 20", got)
	}
	if got := cell(t, f, det, "C3"); got != "10" { // right on value 10
		t.Fatalf("det C3 = %q, want 10", got)
	}
	if got := cell(t, f, det, "D3"); got != "-20" { // wrong on value 20
		t.Fatalf("det D3 = %q, want -20", got)
	}
	if got := cell(t, f, det, "F3"); got != "" { // blank on value 40
		t.Fatalf("det F3 = %q, want empty", got)
	}
	if got := cell(t, f, det, "H3"); got != "20" { // theme1 score column
		t.Fatalf("det H3 theme score = %q, want 20", got)
	}

	// Итог: Дельта (50) ranks above Гамма (20).
	const res = "Итог"
	if got := cell(t, f, res, "A1"); got != "Место" {
		t.Fatalf("res A1 = %q", got)
	}
	if got := cell(t, f, res, "E1"); got != "50" { // RESULT_VALUES high→low
		t.Fatalf("res E1 = %q, want 50", got)
	}
	if got := cell(t, f, res, "A2"); got != "1" {
		t.Fatalf("res A2 place = %q", got)
	}
	if got := cell(t, f, res, "B2"); got != "Дельта" {
		t.Fatalf("res B2 = %q, want Дельта first", got)
	}
	if got := cell(t, f, res, "C2"); got != "50" {
		t.Fatalf("res C2 total = %q, want 50", got)
	}
	if got := cell(t, f, res, "B3"); got != "Гамма" {
		t.Fatalf("res B3 = %q, want Гамма second", got)
	}
}

func TestBuildEKSheets(t *testing.T) {
	mv := MatchView{
		Title:          "Финал",
		Code:           "F1",
		StageTitle:     "Плей-офф",
		StageCode:      "po",
		QuestionValues: [5]int{10, 20, 30, 40, 50},
		Teams: []TeamView{
			{
				Name:  "Эпсилон",
				Total: 40,
				Place: 1,
				Themes: []ThemeView{
					{Answers: [5]string{"right", "", "right", "", ""}, Score: 40},
				},
			},
			{
				Name:  "Дзета",
				Total: -20,
				Place: 2,
				Themes: []ThemeView{
					{Answers: [5]string{"", "wrong", "", "", ""}, Score: -20},
				},
			},
		},
	}
	stages := []stageMatches{{Code: "po", Matches: []MatchView{mv}}}
	f := excelize.NewFile()
	defer f.Close()
	if err := buildEKSheets(f, stages); err != nil {
		t.Fatalf("buildEKSheets: %v", err)
	}
	const sheet = "Плей-офф"
	if idx, err := f.GetSheetIndex(sheet); err != nil || idx < 0 {
		t.Fatalf("missing stage sheet %q (have %v): %v", sheet, f.GetSheetList(), err)
	}
	if got := cell(t, f, sheet, "A1"); got != "Финал" {
		t.Fatalf("A1 match title = %q", got)
	}
	// Row 2 header: Команда | Σ | М | Т1 ...
	if got := cell(t, f, sheet, "A2"); got != "Команда" {
		t.Fatalf("A2 = %q", got)
	}
	if got := cell(t, f, sheet, "D2"); got != "Т1" {
		t.Fatalf("D2 = %q, want Т1", got)
	}
	// Row 3 value sub-headers under theme 1: D..H = 10..50.
	if got := cell(t, f, sheet, "D3"); got != "10" {
		t.Fatalf("D3 value = %q", got)
	}
	// Row 4 first team Эпсилон.
	if got := cell(t, f, sheet, "A4"); got != "Эпсилон" {
		t.Fatalf("A4 = %q", got)
	}
	if got := cell(t, f, sheet, "B4"); got != "40" {
		t.Fatalf("B4 total = %q", got)
	}
	if got := cell(t, f, sheet, "C4"); got != "1" {
		t.Fatalf("C4 place = %q", got)
	}
	if got := cell(t, f, sheet, "D4"); got != "10" { // right on 10
		t.Fatalf("D4 = %q, want 10", got)
	}
	if got := cell(t, f, sheet, "E4"); got != "" { // blank on 20
		t.Fatalf("E4 = %q, want empty", got)
	}
	if got := cell(t, f, sheet, "F4"); got != "30" { // right on 30
		t.Fatalf("F4 = %q, want 30", got)
	}
	// Дзета row 5, wrong on 20 → -20.
	if got := cell(t, f, sheet, "E5"); got != "-20" {
		t.Fatalf("E5 = %q, want -20", got)
	}
}

// TestFestRouterServesXLSX drives the full public route — /fest/{ref}/game/{ref}.xlsx
// — through handleFestRouter, asserting it returns a real workbook with the
// attachment headers rather than falling through to the SPA viewer HTML.
func TestFestRouterServesXLSX(t *testing.T) {
	srv := newAuthTestServer(t)
	_, gameID := scopedAPITestIDs(t, srv)
	if _, err := srv.db.Exec(`update games set slug = 'ek' where id = ?`, gameID); err != nil {
		t.Fatalf("set game slug: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fest/fixture-ek/game/ek.xlsx", nil)
	resp := httptest.NewRecorder()
	srv.handleFestRouter(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("xlsx route status = %d, body %s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := resp.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	f, err := excelize.OpenReader(bytes.NewReader(resp.Body.Bytes()))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()
	if sheets := f.GetSheetList(); len(sheets) == 0 {
		t.Fatalf("workbook has no sheets")
	}
}

func TestExportFileNameAndDisposition(t *testing.T) {
	if got := exportFileName("Финал / 2026", "ek"); got != "Финал - 2026.xlsx" {
		t.Fatalf("exportFileName = %q", got)
	}
	if got := exportFileName("   ", "ksi"); got != "ksi.xlsx" {
		t.Fatalf("exportFileName empty = %q", got)
	}
	cd := contentDispositionAttachment("Финал.xlsx")
	if !strings.Contains(cd, "filename*=UTF-8''") || !strings.Contains(cd, "attachment;") {
		t.Fatalf("disposition = %q", cd)
	}
}
