package server

import "testing"

// TestHumanMB pins the rounding: a handful of bytes must read as "0 МБ" rather
// than spelling out a float's worth of decimals.
func TestHumanMB(t *testing.T) {
	cases := map[int64]string{
		0:        "0 МБ",
		51:       "0 МБ",
		1 << 20:  "1 МБ",
		25 << 20: "25 МБ",
		1503238:  "1.43 МБ",
	}
	for in, want := range cases {
		if got := humanMB(in); got != want {
			t.Errorf("humanMB(%d) = %q, want %q", in, got, want)
		}
	}
}
