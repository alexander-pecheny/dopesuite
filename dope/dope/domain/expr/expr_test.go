package expr

import (
	"math"
	"strings"
	"testing"
)

func eval(t *testing.T, src string, vars Vars) float64 {
	t.Helper()
	e, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	value, err := e.Eval(vars)
	if err != nil {
		t.Fatalf("Eval(%q): %v", src, err)
	}
	return value
}

func TestArithmeticAndPrecedence(t *testing.T) {
	cases := []struct {
		src  string
		want float64
	}{
		{"1 + 2 * 3", 7},
		{"(1 + 2) * 3", 9},
		{"-3 + 1", -2},
		{"7 % 3", 1},
		{"10 / 4", 2.5},
		{"2 < 3", 1},
		{"2 >= 3", 0},
		{"1 && 0 || 1", 1},
		{"!0", 1},
		{"max(2, 3) - min(2, 3)", 1},
		{"abs(0 - 4)", 4},
	}
	for _, c := range cases {
		if got := eval(t, c.src, nil); got != c.want {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}

// The КИНСБФ rule and the СИ rule, the two the model was designed against.
func TestRegulationRules(t *testing.T) {
	// СИ: «за победу в бою — 3 очка, за второе место — 2, за третье — 1»,
	// shared places paying the mean, which 4 − место gives for free.
	si := "4 - place"
	for place, want := range map[float64]float64{1: 3, 2: 2, 3: 1, 1.5: 2.5} {
		if got := eval(t, si, Vars{"place": place}); got != want {
			t.Errorf("СИ place %v = %v, want %v", place, got, want)
		}
	}
	// КИНСБФ: 2 за победу, 1 за ничью, 0 за поражение — the same shape at two
	// seats, because a draw is place 1.5.
	kinsbf := "2 * (2 - place)"
	for place, want := range map[float64]float64{1: 2, 1.5: 1, 2: 0} {
		if got := eval(t, kinsbf, Vars{"place": place}); got != want {
			t.Errorf("КИНСБФ place %v = %v, want %v", place, got, want)
		}
	}
}

// The rule that killed the place-table design: очки depend on взятые, not just
// place, so no place→очки table could express it. 3 за победу, 2 за ничью,
// 1 за поражение со взятым вопросом, 0 за нулевую ничью.
func TestBrainThreeTwoOneZero(t *testing.T) {
	const rule = "taken == 0 ? 0 : (tied > 0 ? 2 : (place == 1 ? 3 : 1))"
	cases := []struct {
		name              string
		place, tied, take float64
		want              float64
	}{
		{"победа", 1, 0, 4, 3},
		{"поражение со взятым", 2, 0, 1, 1},
		{"поражение всухую", 2, 0, 0, 0},
		{"ничья 1:1", 1.5, 1, 1, 2},
		{"ничья 0:0", 1.5, 1, 0, 0},
	}
	for _, c := range cases {
		got := eval(t, rule, Vars{"place": c.place, "tied": c.tied, "taken": c.take})
		if got != c.want {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
}

// A standings-tier rule: доля очков is only computable after the sum, which is
// why the two tiers are not interchangeable.
func TestStandingsTierRule(t *testing.T) {
	const share = "bouts > 0 ? points / (2 * bouts) : 0"
	if got := eval(t, share, Vars{"points": 5, "bouts": 4}); got != 0.625 {
		t.Errorf("points_share = %v, want 0.625", got)
	}
	if got := eval(t, share, Vars{"points": 0, "bouts": 0}); got != 0 {
		t.Errorf("points_share with no bouts = %v, want 0", got)
	}
}

func TestVarsAreReported(t *testing.T) {
	e, err := Parse("taken - opp_taken + max(place, 1)")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"opp_taken", "place", "taken"}
	got := e.Vars()
	if len(got) != len(want) {
		t.Fatalf("Vars() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Vars() = %v, want %v", got, want)
		}
	}
	if strings.Contains(strings.Join(got, ","), "max") {
		t.Error("a function name must not be reported as a variable")
	}
}

// A typo must fail loudly rather than pay everyone the value of a missing name.
func TestUnknownNameIsAnError(t *testing.T) {
	e, err := Parse("takn + 1")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := e.Eval(Vars{"taken": 3}); err == nil {
		t.Fatal("expected an error for an unknown name")
	}
}

func TestSyntaxErrors(t *testing.T) {
	for _, src := range []string{"", "1 +", "(1", "1 ? 2", "max(1)", "1 $ 2", "place 1"} {
		if _, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", src)
		}
	}
}

func TestShortCircuitAvoidsDivisionByZero(t *testing.T) {
	if got := eval(t, "bouts > 0 && points / bouts > 1", Vars{"points": 0, "bouts": 0}); got != 0 {
		t.Errorf("short-circuit = %v, want 0", got)
	}
	e, _ := Parse("points / bouts")
	if _, err := e.Eval(Vars{"points": 1, "bouts": 0}); err == nil {
		t.Error("division by zero must be an error when it is actually evaluated")
	}
}

func TestFractionalPlacesStayExact(t *testing.T) {
	if got := eval(t, "4 - place", Vars{"place": 2.5}); math.Abs(got-1.5) > 1e-9 {
		t.Errorf("shared place = %v, want 1.5", got)
	}
}
