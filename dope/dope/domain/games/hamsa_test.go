package games

import (
	"encoding/json"
	"strconv"
	"testing"
)

func hamsaJSON(t *testing.T, state HamsaState) string {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// hamsaTheme reads a тема written as five characters: R taken, W lost, - not
// played — the transcript's own spelling, which keeps a test case readable.
func hamsaTheme(marks string) HamsaTheme {
	theme := HamsaTheme{Answers: make([]string, HamsaQuestions)}
	for i, r := range marks {
		switch r {
		case 'R':
			theme.Answers[i] = "right"
		case 'W':
			theme.Answers[i] = "wrong"
		}
	}
	return theme
}

func hamsaSide(marks ...string) *HamsaParticipant {
	side := &HamsaParticipant{}
	for _, theme := range marks {
		side.Themes = append(side.Themes, hamsaTheme(theme))
	}
	return side
}

func hamsaBet(amount int, answer string) *HamsaBet {
	return &HamsaBet{Amount: &amount, Answer: answer}
}

func defaultRounds() []HamsaGameRound { return HamsaGameRounds(nil, nil, nil) }

// The rounds are the regulations' own: five темы at ×1, ×2 and ×3, one at ×4,
// over base номиналы of 100 to 500.
func TestHamsaGameRoundsFollowTheRegulations(t *testing.T) {
	rounds := defaultRounds()
	if len(rounds) != 4 {
		t.Fatalf("rounds = %d, want 4", len(rounds))
	}
	if got := []int{rounds[0].Themes, rounds[1].Themes, rounds[2].Themes, rounds[3].Themes}; got[3] != 1 || got[0] != 5 {
		t.Fatalf("themes per round = %v", got)
	}
	if rounds[0].Values[0] != 100 || rounds[0].Values[4] != 500 {
		t.Fatalf("round 1 values = %v", rounds[0].Values)
	}
	if rounds[3].Values[4] != 2000 {
		t.Fatalf("round 4 values = %v, want the base ×4", rounds[3].Values)
	}
	if HamsaThemeCount(rounds) != 16 {
		t.Fatalf("themes = %d, want 16", HamsaThemeCount(rounds))
	}
}

// A scheme that says less than four rounds' worth is padded from the
// regulations rather than refused: `multipliers: [1, 2]` still means four.
func TestHamsaGameRoundsPadShortLists(t *testing.T) {
	rounds := HamsaGameRounds([]int{2}, []int{5}, []int{1, 2})
	if len(rounds) != 1 {
		t.Fatalf("rounds = %d, want the one the scheme wrote", len(rounds))
	}
	if rounds[0].Themes != 2 || rounds[0].Values[0] != 5 || rounds[0].Values[1] != 10 || rounds[0].Values[2] != 1500 {
		t.Fatalf("round = %+v", rounds[0])
	}
}

// A вопрос pays its round's номинал, and the Ставка is added or subtracted
// whole. Σ+ counts what the темы took and leaves the Ставка out: it measures
// what a team knew, not what it gambled.
func TestComputeHamsaResultsScoresThemesAndTheBet(t *testing.T) {
	state := HamsaState{
		Rounds: defaultRounds(),
		Participants: map[string]*HamsaParticipant{
			// Round 1 (×1): 500 taken, 100 lost. Round 2 (×2): 600 taken.
			"7": hamsaSide("W---R", "-----", "-----", "-----", "-----", "--R--"),
			"8": hamsaSide("R----"),
		},
	}
	state.Participants["7"].Bet = hamsaBet(250, HamsaBetRight)
	state.Participants["8"].Bet = hamsaBet(50, HamsaBetWrong)

	results, err := ComputeHamsaResults(hamsaJSON(t, state), []int64{7, 8})
	if err != nil {
		t.Fatalf("ComputeHamsaResults: %v", err)
	}
	if results[0].Total != 500-100+600+250 {
		t.Fatalf("7 total = %d, want 1250", results[0].Total)
	}
	if results[0].Plus != 500+600 {
		t.Fatalf("7 plus = %d, want 1100 — the Ставка stays out", results[0].Plus)
	}
	if results[0].Bet != 250 {
		t.Fatalf("7 bet = %d", results[0].Bet)
	}
	if results[1].Total != 100-50 || results[1].Bet != -50 {
		t.Fatalf("8 = %+v, want a lost Ставка subtracted", results[1])
	}
	// The counts are named after the base номинал, whatever the round paid.
	if results[0].Correct[500] != 1 || results[0].Correct[300] != 1 || results[0].Wrong[100] != 1 {
		t.Fatalf("7 counts = %v / %v", results[0].Correct, results[0].Wrong)
	}
	if results[0].Place != 1 || results[1].Place != 2 {
		t.Fatalf("places = %v %v", results[0].Place, results[1].Place)
	}
	if results[0].First != 1 || results[1].First != 0 {
		t.Fatalf("first = %v %v", results[0].First, results[1].First)
	}
}

// «Команды, набравшие равное количество игровых очков по итогам конкретного
// боя ГЭ, считаются разделившими соответствующие места» — so two level teams
// finish 1.5 and 1.5, and both of them took a первое место.
func TestComputeHamsaResultsSharesALevelPlace(t *testing.T) {
	state := HamsaState{
		Rounds: defaultRounds(),
		Participants: map[string]*HamsaParticipant{
			"1": hamsaSide("R----"),
			"2": hamsaSide("R----"),
			"3": hamsaSide("-----"),
			"4": hamsaSide("W----"),
		},
	}
	results, err := ComputeHamsaResults(hamsaJSON(t, state), []int64{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("ComputeHamsaResults: %v", err)
	}
	want := []float64{1.5, 1.5, 3, 4}
	for i, result := range results {
		if result.Place != want[i] {
			t.Fatalf("places = %v %v %v %v, want %v", results[0].Place, results[1].Place, results[2].Place, results[3].Place, want)
		}
	}
	if results[0].First != 1 || results[1].First != 1 || results[2].First != 0 {
		t.Fatalf("a shared top place is a первое место for both teams: %v", []float64{results[0].First, results[1].First, results[2].First})
	}
}

// Level on the score, the перестрелка decides — and it is held outside Σ, so
// the two teams' totals stay equal.
func TestComputeHamsaResultsRanksOnTheShootout(t *testing.T) {
	state := HamsaState{
		Rounds: defaultRounds(),
		Participants: map[string]*HamsaParticipant{
			"1": hamsaSide("R----"),
			"2": hamsaSide("R----"),
		},
	}
	state.Participants["2"].Shootout = []HamsaTheme{hamsaTheme("R----")}
	results, err := ComputeHamsaResults(hamsaJSON(t, state), []int64{1, 2})
	if err != nil {
		t.Fatalf("ComputeHamsaResults: %v", err)
	}
	if results[0].Total != results[1].Total {
		t.Fatalf("the перестрелка leaked into Σ: %d vs %d", results[0].Total, results[1].Total)
	}
	if results[1].ShootoutTotal != 400 {
		t.Fatalf("shootout = %d, want the Персональный round's номинал", results[1].ShootoutTotal)
	}
	if results[1].Place != 1 || results[0].Place != 2 {
		t.Fatalf("places = %v %v", results[0].Place, results[1].Place)
	}
}

// A seat that entered nothing still took a place, so the seating decides the
// rows rather than the document does.
func TestComputeHamsaResultsPlacesASeatWithNoSection(t *testing.T) {
	state := HamsaState{Rounds: defaultRounds(), Participants: map[string]*HamsaParticipant{"1": hamsaSide("R----")}}
	results, err := ComputeHamsaResults(hamsaJSON(t, state), []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("ComputeHamsaResults: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want one per seat", len(results))
	}
	if results[1].Place != 2.5 || results[2].Place != 2.5 {
		t.Fatalf("the two empty seats share 2-3: %v %v", results[1].Place, results[2].Place)
	}
}

// A Pin is the host's ruling and stands instead of the computed place; the
// первые места are read off the places the бой ended on.
func TestComputeHamsaResultsHonoursAPin(t *testing.T) {
	pin := 1.0
	state := HamsaState{
		Rounds: defaultRounds(),
		Participants: map[string]*HamsaParticipant{
			"1": hamsaSide("R----"),
			"2": {Pin: &pin},
		},
	}
	results, err := ComputeHamsaResults(hamsaJSON(t, state), []int64{1, 2})
	if err != nil {
		t.Fatalf("ComputeHamsaResults: %v", err)
	}
	if results[1].Place != 1 || results[1].First != 1 {
		t.Fatalf("pinned seat = %+v", results[1])
	}
	if results[0].First != 1 {
		t.Fatalf("the seat the marks put first is still first: %+v", results[0])
	}
}

func TestHamsaEmptyStateCarriesItsRoundsAndIsPristine(t *testing.T) {
	raw := HamsaEmptyStateJSON(defaultRounds())
	if HamsaStateStarted(string(raw)) {
		t.Error("a pristine бой is not started")
	}
	state, err := ParseHamsaState(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Rounds) != 4 || state.Rounds[2].Values[0] != 300 {
		t.Fatalf("rounds = %+v", state.Rounds)
	}
	if len(state.Participants) != 0 {
		t.Fatalf("a pristine бой seats nobody in its document: %v", state.Participants)
	}
}

// Every kind of entry counts as a start, because each of them is a host's
// work that a recompile must not reseat.
func TestHamsaStartedSeesEveryEntry(t *testing.T) {
	pin := 2.0
	for name, section := range map[string]*HamsaParticipant{
		"mark":   hamsaSide("R----"),
		"player": {Themes: []HamsaTheme{{Player: 7}}},
		"bet":    {Bet: hamsaBet(300, "")},
		"pin":    {Pin: &pin},
	} {
		state := HamsaState{Rounds: defaultRounds(), Participants: map[string]*HamsaParticipant{"1": section}}
		if !HamsaStateStarted(hamsaJSON(t, state)) {
			t.Errorf("a бой with a %s is started", name)
		}
	}
}

// With no seating the document's own teams are scored, lowest id first — what
// an export or a unit test wants.
func TestComputeHamsaResultsWithoutSeatsReadsTheDocument(t *testing.T) {
	state := HamsaState{Rounds: defaultRounds(), Participants: map[string]*HamsaParticipant{
		"12": hamsaSide("R----"),
		"3":  hamsaSide("-----"),
	}}
	results, err := ComputeHamsaResults(hamsaJSON(t, state), nil)
	if err != nil {
		t.Fatalf("ComputeHamsaResults: %v", err)
	}
	if len(results) != 2 || results[0].Participant != 3 || results[1].Participant != 12 {
		t.Fatalf("results = %v", results)
	}
	if strconv.FormatInt(results[1].Participant, 10) != "12" || results[1].Place != 1 {
		t.Fatalf("12 = %+v", results[1])
	}
}
