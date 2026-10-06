package split

import (
	"encoding/json"
	"os"
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

type evenCase struct {
	Name     string     `json:"name"`
	Total    int64      `json:"total"`
	Joined   []int64    `json:"joined"`
	Payments [][2]int64 `json:"payments"`
	Split    []int64    `json:"split"`
	Want     []int64    `json:"want"`
}

// TestEvenCases is the reference half of the parity check: the editor's
// evenShares reads the same file in web/jstest/split-parity.test.js.
func TestEvenCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/even_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Even []evenCase `json:"even"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Even) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range file.Even {
		t.Run(c.Name, func(t *testing.T) {
			payments := make([]Share, len(c.Payments))
			for i, p := range c.Payments {
				payments[i] = Share{MemberID: p[0], Minor: p[1]}
			}
			got := minors(Even(c.Total, c.Split, PayerOrder(payments, c.Joined)))
			if !equal(got, c.Want) {
				t.Errorf("got %v, want %v", got, c.Want)
			}
			var sum int64
			for _, v := range got {
				sum += v
			}
			if sum != c.Total {
				t.Errorf("shares sum to %d, want the whole total %d", sum, c.Total)
			}
		})
	}
	if got := Even(1000, nil, nil); got != nil {
		t.Errorf("Even with no members = %v, want nil", got)
	}
}

func TestPayerOrder(t *testing.T) {
	payments := []Share{{3, 500}, {1, 300}, {2, 500}, {4, 0}}
	if got := PayerOrder(payments, []int64{1, 2, 3, 4}); !equal(got, []int64{2, 3, 1}) {
		t.Errorf("PayerOrder = %v, want [2 3 1]", got)
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
