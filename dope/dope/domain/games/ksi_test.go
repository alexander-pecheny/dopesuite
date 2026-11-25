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

func TestKSIStickerOptions(t *testing.T) {
	// Team А: a ×2 theme with a right 50; team Б: two plain themes with a
	// right 50 and a right 30, and a wrong 10. Total: А 100, Б 70.
	state := `{"participants":[{"number":1,"name":"А"},{"number":2,"name":"Б"}],
	  "stickers":[["x2","neutral"],["","neutral"],["",""]],
	  "themes":[{"answers":[["","","","","right"],["","","","","right"]]},
	            {"answers":[["","","","",""],["wrong","","right","",""]]},
	            {"answers":[["","","","",""],["","","","",""]]}]}`
	values := []int{10, 20, 30, 40, 50}
	types := `"types":[{"id":"neutral"},{"id":"x2"},{"id":"emptywrong"}]`

	// The default: the plus sums what the stickers scored, doubling included,
	// and А's second theme, without a sticker, is not scored.
	ranked, err := ComputeKSIResults(`{"stickers":{`+types+`}}`, state, values)
	if err != nil {
		t.Fatal(err)
	}
	if ranked[0].Index != 0 || ranked[0].Total != 100 || ranked[0].Plus != 100 {
		t.Errorf("default: first = %+v, want А with 100/100", ranked[0])
	}

	// nominal: the plus is the right answers' nominals; missing: А's sheet with
	// no sticker scores as «Пустой = минус», −150 for five empty answers. The
	// third theme is not played yet: nobody's sheet in it costs anything.
	ranked, err = ComputeKSIResults(`{"stickers":{`+types+`,"plus":"nominal","missing":"emptywrong"}}`, state, values)
	if err != nil {
		t.Fatal(err)
	}
	byIndex := map[int]KSIResultsTeam{}
	for _, team := range ranked {
		byIndex[team.Index] = team
	}
	if a := byIndex[0]; a.Total != 100-150 || a.Plus != 50 {
		t.Errorf("А = total %d plus %d, want −50 and 50", a.Total, a.Plus)
	}
	if b := byIndex[1]; b.Total != 70 || b.Plus != 80 {
		t.Errorf("Б = total %d plus %d, want 70 and 80", b.Total, b.Plus)
	}
}
