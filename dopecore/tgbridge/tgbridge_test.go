package tgbridge

import "testing"

func TestLooksLikeRegisterCode(t *testing.T) {
	cases := map[string]bool{
		"ABCD234567": true, "ABC": false, "abcd2345": false, "ABCD1890": false,
		"A234567B": true, "": false,
	}
	for code, want := range cases {
		if got := LooksLikeRegisterCode(code); got != want {
			t.Errorf("%q: got %v want %v", code, got, want)
		}
	}
}
