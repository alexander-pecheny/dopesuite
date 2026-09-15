package split

import (
	"errors"
	"math/big"
	"testing"
)

func minors(shares []Share) []int64 {
	out := make([]int64, len(shares))
	for i, s := range shares {
		out[i] = s.Minor
	}
	return out
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEven(t *testing.T) {
	cases := []struct {
		name    string
		total   int64
		members []int64
		payers  []int64
		want    []int64
	}{
		{
			// 10.00 across three: the odd cent goes to the payer, not to the
			// first name on the list.
			name: "odd cent to the payer", total: 1000,
			members: []int64{1, 2, 3}, payers: []int64{2},
			want: []int64{333, 334, 333},
		},
		{
			// Nobody named a payer (the ledger always does; a bare split form
			// need not): split order decides.
			name: "no payer falls back to split order", total: 1000,
			members: []int64{1, 2, 3}, payers: nil,
			want: []int64{334, 333, 333},
		},
		{
			// Two payers: the bigger Payment comes first, so its holder takes
			// the first spare cent.
			name: "payers in descending payment", total: 100,
			members: []int64{1, 2, 3}, payers: []int64{3, 1},
			want: []int64{33, 33, 34},
		},
		{
			// Four spare minor units across six, two payers first.
			name: "several spare units", total: 1000,
			members: []int64{1, 2, 3, 4, 5, 6}, payers: []int64{5, 2},
			want: []int64{167, 167, 167, 166, 167, 166},
		},
		{
			name: "exact division leaves nothing over", total: 900,
			members: []int64{1, 2, 3}, payers: []int64{1},
			want: []int64{300, 300, 300},
		},
		{
			name: "one member takes the lot", total: 1234,
			members: []int64{7}, payers: []int64{7},
			want: []int64{1234},
		},
		{
			// A zero-exponent currency is not a special case here: the minor
			// unit is the yen, and 1000 yen across three is 334/333/333.
			name: "JPY minor units are whole yen", total: 1000,
			members: []int64{1, 2, 3}, payers: []int64{1},
			want: []int64{334, 333, 333},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := minors(Even(c.total, c.members, c.payers))
			if !equal(got, c.want) {
				t.Errorf("Even(%d, %v, %v) = %v, want %v", c.total, c.members, c.payers, got, c.want)
			}
			var sum int64
			for _, v := range got {
				sum += v
			}
			if sum != c.total {
				t.Errorf("shares sum to %d, want the whole total %d", sum, c.total)
			}
		})
	}
	if got := Even(1000, nil, nil); got != nil {
		t.Errorf("Even with no members = %v, want nil", got)
	}
}

func pct(values ...string) []*big.Rat {
	out := make([]*big.Rat, len(values))
	for i, v := range values {
		r, ok := new(big.Rat).SetString(v)
		if !ok {
			panic("bad percentage in test: " + v)
		}
		out[i] = r
	}
	return out
}

func TestByPercent(t *testing.T) {
	cases := []struct {
		name     string
		total    int64
		members  []int64
		percents []*big.Rat
		payers   []int64
		want     []int64
	}{
		{
			// 33.33 / 66.67 of 10.00 is 3.333 / 6.667: the spare cent follows
			// the bigger remainder, not the payer.
			name: "largest remainder wins before the order does", total: 1000,
			members: []int64{1, 2}, percents: pct("33.33", "66.67"), payers: []int64{1},
			want: []int64{333, 667},
		},
		{
			name: "round percentages need no tie-break", total: 1000,
			members: []int64{1, 2, 3, 4}, percents: pct("25", "25", "25", "25"), payers: []int64{4},
			want: []int64{250, 250, 250, 250},
		},
		{
			// Three equal thirds of 10.00: equal remainders, so the payer takes
			// the cent.
			name: "equal remainders fall to the payer", total: 1000,
			members:  []int64{1, 2, 3},
			percents: []*big.Rat{big.NewRat(100, 3), big.NewRat(100, 3), big.NewRat(100, 3)},
			payers:   []int64{3},
			want:     []int64{333, 333, 334},
		},
		{
			// Percentages that do not reach 100 leave the rest Unclaimed —
			// a normal state, not an error.
			name: "a partial split leaves the rest unclaimed", total: 1000,
			members: []int64{1}, percents: pct("50"), payers: []int64{1},
			want: []int64{500},
		},
		{
			name: "zero percent is a member who claims nothing", total: 1000,
			members: []int64{1, 2}, percents: pct("0", "100"), payers: []int64{1},
			want: []int64{0, 1000},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ByPercent(c.total, c.members, c.percents, c.payers)
			if err != nil {
				t.Fatalf("ByPercent: %v", err)
			}
			if !equal(minors(got), c.want) {
				t.Errorf("ByPercent = %v, want %v", minors(got), c.want)
			}
			var sum int64
			for _, s := range got {
				sum += s.Minor
			}
			if sum > c.total {
				t.Errorf("shares sum to %d, past the total %d", sum, c.total)
			}
		})
	}
}

func TestByPercentRefusals(t *testing.T) {
	if _, err := ByPercent(1000, []int64{1, 2}, pct("60", "60"), nil); !errors.Is(err, ErrPercent) {
		t.Errorf("percentages past 100 = %v, want ErrPercent", err)
	}
	if _, err := ByPercent(1000, []int64{1}, pct("-1"), nil); !errors.Is(err, ErrPercent) {
		t.Errorf("negative percentage = %v, want ErrPercent", err)
	}
	if _, err := ByPercent(1000, []int64{1, 2}, pct("50"), nil); !errors.Is(err, ErrWeights) {
		t.Errorf("one percentage short = %v, want ErrWeights", err)
	}
}

// Two people holding the same list must see the same numbers, whatever order
// the payers arrive in — the property the whole ordering rule exists for.
func TestDeterministic(t *testing.T) {
	first := minors(Even(1000, []int64{4, 9, 2}, []int64{9, 2}))
	for range 20 {
		if got := minors(Even(1000, []int64{4, 9, 2}, []int64{9, 2})); !equal(got, first) {
			t.Fatalf("same input gave %v then %v", first, got)
		}
	}
	if !equal(first, []int64{333, 334, 333}) {
		t.Errorf("Even = %v, want 333/334/333 with 9 paying most", first)
	}
}
