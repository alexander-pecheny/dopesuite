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

// Поделённое место — среднее арифметическое поделённых мест, а не меньшее из
// них: два первых получают по 1,5. Место — это то, за что Структура платит
// очки, и только среднее не зависит от того, как легли ничьи.
func TestKSISharedPlaceIsTheMean(t *testing.T) {
	scheme := `{"themes":[{},{}],"participants":[{"number":1,"name":"А"},{"number":2,"name":"Б"},{"number":3,"name":"В"}]}`
	// А и Б берут по одному вопросу на 10, В не берёт ничего.
	state := `{"participants":[{"number":1,"name":"А"},{"number":2,"name":"Б"},{"number":3,"name":"В"}],
	  "themes":[{"answers":[["right","","","",""],["right","","","",""],["","","","",""]]},
	            {"answers":[["","","","",""],["","","","",""],["","","","",""]]}]}`
	ranked, err := ComputeKSIResults(scheme, state, []int{10, 20, 30, 40, 50})
	if err != nil {
		t.Fatalf("ComputeKSIResults: %v", err)
	}
	if len(ranked) != 3 {
		t.Fatalf("строк = %d, want 3", len(ranked))
	}
	if ranked[0].Place != 1.5 || ranked[1].Place != 1.5 {
		t.Errorf("поделённое первое = %v и %v, want 1.5", ranked[0].Place, ranked[1].Place)
	}
	if ranked[2].Place != 3 {
		t.Errorf("третье место = %v, want 3", ranked[2].Place)
	}
}
