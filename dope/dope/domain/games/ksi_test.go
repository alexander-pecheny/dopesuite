package games

import "testing"

func TestKSIStickerMarkValue(t *testing.T) {
	const v = 30
	cases := []struct {
		sticker string
		mark    string
		want    int
	}{
		// neutral scores like a regular KSI theme.
		{KSIStickerNeutral, "right", v},
		{KSIStickerNeutral, "wrong", -v},
		{KSIStickerNeutral, "", 0},
		// x2 doubles both right and wrong.
		{KSIStickerX2, "right", 2 * v},
		{KSIStickerX2, "wrong", -2 * v},
		{KSIStickerX2, "", 0},
		// no-wrong zeroes out wrong answers.
		{KSIStickerNoWrong, "right", v},
		{KSIStickerNoWrong, "wrong", 0},
		{KSIStickerNoWrong, "", 0},
		// empty = wrong: an empty answer is penalised like a wrong one.
		{KSIStickerEmptyWrong, "right", v},
		{KSIStickerEmptyWrong, "wrong", -v},
		{KSIStickerEmptyWrong, "", -v},
		// unknown sticker id falls back to neutral.
		{"bogus", "right", v},
		{"bogus", "wrong", -v},
	}
	for _, c := range cases {
		if got := KSIStickerMarkValue(c.sticker, c.mark, v); got != c.want {
			t.Errorf("KSIStickerMarkValue(%q,%q,%d) = %d, want %d", c.sticker, c.mark, v, got, c.want)
		}
	}
}
