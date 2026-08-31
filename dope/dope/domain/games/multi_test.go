package games

import (
	"reflect"
	"testing"
)

func TestParseMultiGamesReadsNamesAndDomains(t *testing.T) {
	games, err := ParseMultiGames(`
Фоторяд:   {0,1}x10
Логика:    {0,3}x2 {0,5}
Штрафной:  {-1,0,1}x2
Кроссворд: {0-3}
`)
	if err != nil {
		t.Fatalf("ParseMultiGames: %v", err)
	}
	if len(games) != 4 {
		t.Fatalf("got %d мини-игры, want 4", len(games))
	}
	if games[0].Name != "Фоторяд" || len(games[0].Columns) != 10 {
		t.Fatalf("game 0 = %q with %d columns", games[0].Name, len(games[0].Columns))
	}
	if !reflect.DeepEqual(games[0].Columns[3].Values, []int{0, 1}) {
		t.Fatalf("Фоторяд column 3 = %v", games[0].Columns[3].Values)
	}
	// Specs concatenate: 3/3/5 is two columns of {0,3} and one of {0,5}.
	if len(games[1].Columns) != 3 {
		t.Fatalf("Логика has %d columns, want 3", len(games[1].Columns))
	}
	if !reflect.DeepEqual(games[1].Columns[2].Values, []int{0, 5}) {
		t.Fatalf("Логика column 2 = %v", games[1].Columns[2].Values)
	}
	if !reflect.DeepEqual(games[2].Columns[0].Values, []int{-1, 0, 1}) {
		t.Fatalf("Штрафной column 0 = %v", games[2].Columns[0].Values)
	}
	// A range is every integer between its ends.
	if !reflect.DeepEqual(games[3].Columns[0].Values, []int{0, 1, 2, 3}) {
		t.Fatalf("Кроссворд column 0 = %v", games[3].Columns[0].Values)
	}
}

// «|» closes a блок of the sheet; columns remember theirs.
func TestParseMultiGamesReadsBlocks(t *testing.T) {
	games, err := ParseMultiGames("Медиа: {0,1}x2 | {0,2} | {0,3}\nПесни: {0,1}x3")
	if err != nil {
		t.Fatalf("ParseMultiGames: %v", err)
	}
	var blocks []int
	for _, column := range games[0].Columns {
		blocks = append(blocks, column.Block)
	}
	if !reflect.DeepEqual(blocks, []int{0, 0, 1, 2}) {
		t.Fatalf("Медиа blocks = %v", blocks)
	}
	for i, column := range games[1].Columns {
		if column.Block != 0 {
			t.Fatalf("Песни column %d in block %d", i, column.Block)
		}
	}
}

func TestParseMultiGamesRefusesNonsense(t *testing.T) {
	for _, src := range []string{
		"Медиа: | {0,1}",
		"Медиа: {0,1} |",
		"Медиа: {0,1} | | {0,2}",
		"Фоторяд: {0,1}x0",
		"Фоторяд: {}",
		"Фоторяд: {3-0}",
		"Фоторяд: {0,x}",
		"Фоторяд:",
		"{0,1}x10",
	} {
		if _, err := ParseMultiGames(src); err == nil {
			t.Errorf("ParseMultiGames(%q) = nil error, want a complaint", src)
		}
	}
}

// Σ+ is the sum of the positive cells; it is worth a column only where some
// мини-игра can go negative at all.
func TestMultiSignedReportsWhetherAnyColumnAdmitsAMinus(t *testing.T) {
	plain, _ := ParseMultiGames("Фоторяд: {0,1}x3")
	if MultiSigned(plain) {
		t.Error("a game of {0,1} is not signed")
	}
	penal, _ := ParseMultiGames("Фоторяд: {0,1}x3\nШтраф: {-1,0,1}x2")
	if !MultiSigned(penal) {
		t.Error("a game with a {-1,0,1} мини-игра is signed")
	}
}

func TestComputeMultiResultsSumsAndRanks(t *testing.T) {
	scheme := `{"minigames":[
		{"name":"Фоторяд","columns":[{"values":[0,1]},{"values":[0,1]},{"values":[0,1]}]},
		{"name":"Штраф","columns":[{"values":[-1,0,1]},{"values":[-1,0,1]}]}
	]}`
	state := `{"participants":[{"number":1,"name":"А"},{"number":2,"name":"Б"},{"number":3,"name":"В"}],
		"games":[
			{"cells":[[1,1,1],[1,0,1],[1,1,0]]},
			{"cells":[[1,1],[-1,-1],[1,-1]]}
		]}`
	ranked, err := ComputeMultiResults(scheme, state)
	if err != nil {
		t.Fatalf("ComputeMultiResults: %v", err)
	}
	if len(ranked) != 3 {
		t.Fatalf("got %d rows", len(ranked))
	}
	// А: 3 + 2 = 5. Б: 2 − 2 = 0, Σ+ 2. В: 2 + 0 = 2, Σ+ 3.
	if ranked[0].Index != 0 || ranked[0].Total != 5 || ranked[0].Plus != 5 || ranked[0].Place != 1 {
		t.Fatalf("first = %+v", ranked[0])
	}
	if ranked[1].Index != 2 || ranked[1].Total != 2 || ranked[1].Plus != 3 {
		t.Fatalf("second = %+v", ranked[1])
	}
	if ranked[2].Total != 0 || ranked[2].Plus != 2 {
		t.Fatalf("third = %+v", ranked[2])
	}
	// Per-мини-игра subtotals ride along, so a scheme may rank on one.
	if ranked[0].Games[0] != 3 || ranked[0].Games[1] != 2 {
		t.Fatalf("subtotals = %v", ranked[0].Games)
	}
}

// Equal totals share the mean of the places they cover, as every dope format
// does; a fest that wants a tiebreak names one.
func TestComputeMultiResultsSharesAPlaceAndObeysTheSchemesSorting(t *testing.T) {
	scheme := `{"minigames":[
		{"name":"Раз","columns":[{"values":[0,1]},{"values":[0,1]}]},
		{"name":"Два","columns":[{"values":[0,1]},{"values":[0,1]}]}
	]}`
	state := `{"participants":[{"number":1,"name":"А"},{"number":2,"name":"Б"}],
		"games":[{"cells":[[1,1],[0,0]]},{"cells":[[0,0],[1,1]]}]}`

	shared, err := ComputeMultiResults(scheme, state)
	if err != nil {
		t.Fatalf("ComputeMultiResults: %v", err)
	}
	if shared[0].Place != 1.5 || shared[1].Place != 1.5 {
		t.Fatalf("places = %v %v, want 1.5 each", shared[0].Place, shared[1].Place)
	}

	sorted := `{"sorting":["total","game1"],"minigames":[
		{"name":"Раз","columns":[{"values":[0,1]},{"values":[0,1]}]},
		{"name":"Два","columns":[{"values":[0,1]},{"values":[0,1]}]}
	]}`
	ranked, err := ComputeMultiResults(sorted, state)
	if err != nil {
		t.Fatalf("ComputeMultiResults: %v", err)
	}
	if ranked[0].Index != 0 || ranked[0].Place != 1 || ranked[1].Place != 2 {
		t.Fatalf("ranked = %+v", ranked)
	}
}

// A мини-игра may be scored against the best result in it rather than raw —
// «→0..100» — so two мини-игры of quite different scales weigh the same in the
// Итог. Below zero is zero: a team that finished on minus scores nothing for
// that мини-игра rather than dragging its Итог down.
func TestParseMultiGamesReadsTheNormalisedForm(t *testing.T) {
	games, err := ParseMultiGames("Медиа-эрудит →0..100: {-10,0,10}x2 {-20,0,20}\nПесни ->0..100: {0,1}x3\nСырая: {0,1}x2")
	if err != nil {
		t.Fatalf("ParseMultiGames: %v", err)
	}
	if games[0].Name != "Медиа-эрудит" || !games[0].Normalized {
		t.Fatalf("game 0 = %q normalized=%v", games[0].Name, games[0].Normalized)
	}
	if len(games[0].Columns) != 3 {
		t.Fatalf("Медиа-эрудит has %d columns", len(games[0].Columns))
	}
	// The arrow may be typed either way; a мини-игра without one stays raw.
	if games[1].Name != "Песни" || !games[1].Normalized {
		t.Fatalf("game 1 = %q normalized=%v", games[1].Name, games[1].Normalized)
	}
	if games[2].Normalized {
		t.Error("a мини-игра with no arrow is raw")
	}
}

// The divisor is the best among the teams in the зачёт: a team that refused to
// play cannot set the scale for everyone else.
func TestComputeMultiResultsNormalisesAgainstTheBestRankedTeam(t *testing.T) {
	scheme := `{"minigames":[
		{"name":"Эрудит","normalized":true,"columns":[{"values":[-10,0,10]},{"values":[-20,0,20]}]},
		{"name":"Песни","normalized":true,"columns":[{"values":[0,1]},{"values":[0,1]}]}
	]}`
	state := `{"participants":[{"number":1,"name":"А"},{"number":2,"name":"Б"},{"number":3,"name":"В"},{"number":4,"name":"Г"}],
		"declined":{"n4":true},
		"games":[
			{"cells":[[10,20],[10,0],[-10,-20],[10,20]]},
			{"cells":[[1,1],[1,0],[0,0],[1,1]]}
		]}`
	ranked, err := ComputeMultiResults(scheme, state)
	if err != nil {
		t.Fatalf("ComputeMultiResults: %v", err)
	}
	by := map[int]MultiResultsTeam{}
	for _, row := range ranked {
		by[row.Index] = row
	}
	// А is the best in both: 30 of 30 and 2 of 2 → 100 + 100.
	if by[0].Total != 200 || by[0].Games[0] != 100 || by[0].Games[1] != 100 {
		t.Fatalf("А = %+v", by[0])
	}
	// Б: 10 of 30 → 33.33…, 1 of 2 → 50.
	if got := by[1].Games[0]; got < 33.33 || got > 33.34 {
		t.Fatalf("Б эрудит = %v, want 100/3", got)
	}
	if by[1].Games[1] != 50 {
		t.Fatalf("Б песни = %v", by[1].Games[1])
	}
	// В finished on minus: nought for that мини-игра, never below.
	if by[2].Games[0] != 0 || by[2].Total != 0 {
		t.Fatalf("В = %+v, want 0 rather than a negative", by[2])
	}
	// Г declined, so Г is unranked — and Г's 30 did not set the scale, А's did.
	if _, ranked := by[3]; ranked {
		t.Error("a declined team stays out of the ranking")
	}
	// The raw subtotals ride along beside the normalised ones.
	if by[0].Raw[0] != 30 || by[1].Raw[0] != 10 {
		t.Fatalf("raw = %v / %v", by[0].Raw, by[1].Raw)
	}
}

// A мини-игра nobody scored in has no scale to speak of, so it pays nobody
// rather than dividing by zero.
func TestComputeMultiResultsSurvivesAnUnplayedMinigame(t *testing.T) {
	scheme := `{"minigames":[{"name":"Пусто","normalized":true,"columns":[{"values":[0,1]}]}]}`
	state := `{"participants":[{"number":1,"name":"А"}],"games":[{"cells":[[0]]}]}`
	ranked, err := ComputeMultiResults(scheme, state)
	if err != nil {
		t.Fatalf("ComputeMultiResults: %v", err)
	}
	if ranked[0].Total != 0 || ranked[0].Games[0] != 0 {
		t.Fatalf("row = %+v", ranked[0])
	}
}
