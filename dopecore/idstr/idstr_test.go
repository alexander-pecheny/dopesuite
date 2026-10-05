package idstr

import "testing"

func TestRoundTrip(t *testing.T) {
	for _, id := range []int64{0, 7, -42, 1 << 62} {
		got, err := Parse(Format(id))
		if err != nil || got != id {
			t.Fatalf("Parse(Format(%d)) = %d, %v", id, got, err)
		}
	}
	if _, err := Parse("12x"); err == nil {
		t.Fatal("Parse accepted 12x")
	}
	if got := string(Append([]byte("id="), 9)); got != "id=9" {
		t.Fatalf("Append = %q", got)
	}
}
