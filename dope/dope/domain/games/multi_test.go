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

func TestParseMultiGamesRefusesNonsense(t *testing.T) {
	for _, src := range []string{
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
