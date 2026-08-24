package rank

import "testing"

// The canonical values of the fractional-indexing algorithm, the same ones
// web/ts/rank.ts produces — Go and the browser must agree on where a card lands.
func TestBetweenKnownKeys(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"", "", "a0"},
		{"a0", "", "a1"},
		{"a1", "", "a2"},
		{"a0", "a1", "a0V"},
		{"", "a0", "Zz"},
		{"a0V", "a1", "a0l"},
		{"Zz", "a0", "ZzV"},
	}
	for _, c := range cases {
		got, err := Between(c.a, c.b)
		if err != nil {
			t.Fatalf("Between(%q, %q): %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("Between(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestBetweenStaysOrdered(t *testing.T) {
	lo, _ := Between("", "")
	hi, _ := Between(lo, "")
	seen := map[string]bool{lo: true, hi: true}
	for i := 0; i < 50; i++ {
		mid, err := Between(lo, hi)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if !(lo < mid && mid < hi) {
			t.Fatalf("step %d: %q < %q < %q violated", i, lo, mid, hi)
		}
		if seen[mid] {
			t.Fatalf("step %d: duplicate key %q", i, mid)
		}
		seen[mid] = true
		hi = mid
	}
}

func TestBetweenRejectsInverted(t *testing.T) {
	if _, err := Between("a1", "a0"); err == nil {
		t.Fatal("Between(a1, a0) should fail")
	}
	if _, err := Between("a0", "a0"); err == nil {
		t.Fatal("Between(a0, a0) should fail")
	}
}

// After is the server's append (Trello upload); it is Between(prev, "").
func TestAfterMatchesBetween(t *testing.T) {
	for _, prev := range []string{"", "a0", "az", "Zz"} {
		got, err := After(prev)
		if err != nil {
			t.Fatalf("After(%q): %v", prev, err)
		}
		want, err := Between(prev, "")
		if err != nil {
			t.Fatalf("Between(%q, \"\"): %v", prev, err)
		}
		if got != want {
			t.Errorf("After(%q) = %q, Between = %q", prev, got, want)
		}
	}
}
