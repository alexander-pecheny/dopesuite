package roman

import "testing"

func TestUpper(t *testing.T) {
	for n, want := range map[int]string{0: "", 1: "I", 4: "IV", 9: "IX", 14: "XIV", 40: "XL", 1994: "MCMXCIV"} {
		if got := Upper(n); got != want {
			t.Errorf("Upper(%d) = %q, want %q", n, got, want)
		}
	}
}
