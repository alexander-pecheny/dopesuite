package games

import (
	"encoding/json"
	"testing"
)

func troikaSide(themes ...[][]string) TroikaSide {
	side := TroikaSide{}
	for _, theme := range themes {
		side.Themes = append(side.Themes, TroikaTheme{Order: []int64{1, 2, 3}, Answers: theme})
	}
	return side
}

func troikaJSON(t *testing.T, state TroikaState) string {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Every player answers every вопрос the team plays, and every correct answer
// pays that вопрос's нарицательная on its own — «за каждый правильный ответ
// команда получает количество баллов, равное стоимости вопроса». So a вопрос
// all three took pays three times over.
func TestComputeTroikaResultsPaysEveryCorrectAnswer(t *testing.T) {
	r := [][]string{{"right", "right", "right"}, {"right", "wrong", ""}, {"", "", ""}}
	state := TroikaState{
		Values: []int{1, 2},
		Sides: []TroikaSide{
			troikaSide(r, [][]string{{"right", "", ""}, {"", "", ""}, {"", "", ""}}),
			troikaSide([][]string{{"wrong", "wrong", "wrong"}, {"", "", ""}, {"", "", ""}}, nil),
		},
	}
	results, err := ComputeTroikaResults(troikaJSON(t, state))
	if err != nil {
		t.Fatalf("ComputeTroikaResults: %v", err)
	}
	// Тема 1 за 1: 3 + 1 = 4 правильных → 4 очка. Тема 2 за 2: 1 → 2 очка.
	if results[0].Total != 6 || results[0].Correct != 5 {
		t.Fatalf("side 0 = %+v, want total 6 correct 5", results[0])
	}
	if results[1].Total != 0 || results[1].Correct != 0 {
		t.Fatalf("side 1 = %+v", results[1])
	}
	if results[0].Place != 1 || results[1].Place != 2 {
		t.Fatalf("places = %v %v", results[0].Place, results[1].Place)
	}
}

// A бой may end drawn — the регламент pays half a рейтинговый балл for it —
// so the two sides share the place rather than being split arbitrarily.
func TestComputeTroikaResultsSharesAPlaceOnADraw(t *testing.T) {
	one := [][]string{{"right", "", ""}, {"", "", ""}, {"", "", ""}}
	state := TroikaState{Values: []int{1}, Sides: []TroikaSide{troikaSide(one), troikaSide(one)}}
	results, err := ComputeTroikaResults(troikaJSON(t, state))
	if err != nil {
		t.Fatalf("ComputeTroikaResults: %v", err)
	}
	if results[0].Place != 1.5 || results[1].Place != 1.5 {
		t.Fatalf("places = %v %v, want 1.5 each", results[0].Place, results[1].Place)
	}
}

func TestTroikaEmptyStateIsSizedAndPristine(t *testing.T) {
	raw := TroikaEmptyStateJSON(TroikaThemeValues(6, []int{1, 1, 1, 2, 2, 3}))
	if TroikaStateStarted(string(raw)) {
		t.Error("a pristine бой is not started")
	}
	var state TroikaState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Values) != 6 || state.Values[5] != 3 {
		t.Fatalf("values = %v", state.Values)
	}
	if len(state.Sides) != 2 || len(state.Sides[0].Themes) != 6 {
		t.Fatalf("sides = %d, themes = %d", len(state.Sides), len(state.Sides[0].Themes))
	}
	theme := state.Sides[0].Themes[0]
	if len(theme.Order) != TroikaChairs || len(theme.Answers) != TroikaThemeQuestions ||
		len(theme.Answers[0]) != TroikaChairs {
		t.Fatalf("theme = %+v", theme)
	}
}

// An unauthored theme value is the «темы за 1 балл» every published Троечка
// has played; a shorter list is padded rather than refused.
func TestTroikaThemeValuesPad(t *testing.T) {
	if got := TroikaThemeValues(4, []int{2, 3}); got[0] != 2 || got[1] != 3 || got[2] != 1 || got[3] != 1 {
		t.Fatalf("TroikaThemeValues = %v", got)
	}
	if got := TroikaThemeValues(0, nil); len(got) != TroikaThemeCount {
		t.Fatalf("default themes = %d", len(got))
	}
}

// A seated player alone is data: a бой whose lineup a host has entered must
// survive a пересев, which is what Started guards.
func TestTroikaStartedSeesASeatedPlayer(t *testing.T) {
	state := TroikaState{Values: []int{1}, Sides: []TroikaSide{
		{Themes: []TroikaTheme{{Order: []int64{7, 0, 0}, Answers: [][]string{{"", "", ""}}}}},
	}}
	if !TroikaStateStarted(troikaJSON(t, state)) {
		t.Error("a бой with a lineup is started")
	}
}
