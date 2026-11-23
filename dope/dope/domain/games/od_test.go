package games

import (
	"slices"
	"testing"
)

func TestComputeODResults(t *testing.T) {
	// 4 teams, 2 tours of 2 questions. Questions 0-2 completed, question 3 not.
	// took (by number): q0:{1,2,3} q1:{1,2} q2:{1} q3 not completed.
	// Totals: T1(n1)=3, T2(n2)=2, T3(n3)=1, T4(n4)=0.
	scheme := `{"tourComp":[2,2]}`
	state := `{
		"teams":[
			{"name":"A","city":"X","number":1},
			{"name":"B","city":"Y","number":2},
			{"name":"C","city":"Z","number":3},
			{"name":"D","city":"W","number":4}
		],
		"entries":[[1,2,3],[1,2],[1],[1,2,3,4]],
		"completed":[true,true,true,false]
	}`

	res, err := ComputeODResults(scheme, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.TourComp) != 2 || res.TourComp[0] != 2 || res.TourComp[1] != 2 {
		t.Fatalf("tourComp = %v", res.TourComp)
	}
	if len(res.Teams) != 4 {
		t.Fatalf("want 4 teams, got %d", len(res.Teams))
	}

	// Ranked: A(3) > B(2) > C(1) > D(0).
	want := []struct {
		number     int64
		place      string
		total      int
		tourTotals []int
		mask       string
	}{
		// 4 questions total (q3 not completed → 0 for everyone).
		{1, "1", 3, []int{2, 1}, "1110"}, // q0,q1 (tour1), q2 (tour2)
		{2, "2", 2, []int{2, 0}, "1100"}, // q0,q1
		{3, "3", 1, []int{1, 0}, "1000"}, // q0
		{4, "4", 0, []int{0, 0}, "0000"},
	}
	for i, w := range want {
		got := res.Teams[i]
		if got.Number != w.number || got.Place != w.place || got.Total != w.total {
			t.Errorf("rank %d: got number=%d place=%q total=%d, want number=%d place=%q total=%d",
				i, got.Number, got.Place, got.Total, w.number, w.place, w.total)
		}
		if len(got.TourTotals) != 2 || got.TourTotals[0] != w.tourTotals[0] || got.TourTotals[1] != w.tourTotals[1] {
			t.Errorf("rank %d: tourTotals = %v, want %v", i, got.TourTotals, w.tourTotals)
		}
		if got.Mask != w.mask {
			t.Errorf("rank %d: mask = %q, want %q", i, got.Mask, w.mask)
		}
	}

	// Rating for team A: took q0 (3 takers), q1 (2 takers), q2 (1 taker), teamCount=4.
	// R = (4-3+1)+(4-2+1)+(4-1+1) = 2+3+4 = 9.
	if res.Teams[0].Rating != 9 {
		t.Errorf("team A rating = %d, want 9", res.Teams[0].Rating)
	}
}

func TestComputeODResultsTiesAndEmpty(t *testing.T) {
	scheme := `{"tourComp":[2]}`

	// No question completed → blank places.
	empty, err := ComputeODResults(scheme, `{
		"teams":[{"name":"A","number":1},{"name":"B","number":2}],
		"entries":[[1,2],[1]],
		"completed":[false,false]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tm := range empty.Teams {
		if tm.Place != "" {
			t.Errorf("expected blank place before any completed question, got %q", tm.Place)
		}
	}

	// Two teams tied on total → shared "1–2" label.
	tied, err := ComputeODResults(scheme, `{
		"teams":[{"name":"A","number":1},{"name":"B","number":2},{"name":"C","number":3}],
		"entries":[[1,2],[1,2]],
		"completed":[true,true]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if tied.Teams[0].Place != "1–2" || tied.Teams[1].Place != "1–2" {
		t.Errorf("tie labels = %q,%q, want 1–2,1–2", tied.Teams[0].Place, tied.Teams[1].Place)
	}
	if tied.Teams[2].Place != "3" || tied.Teams[2].Total != 0 {
		t.Errorf("third team = place %q total %d, want place 3 total 0", tied.Teams[2].Place, tied.Teams[2].Total)
	}
}

func TestComputeODResultsMinus(t *testing.T) {
	// Тминное поле in small: two tours of three, a wrong answer −3, a whole
	// tour +3, an empty tour charged 3/3 = 1 wrong.
	scheme := `{"tourComp":[3,3],"minus":{"wrong":3,"allBonus":3,"emptyTour":true}}`
	state := `{
		"teams":[{"name":"A","number":1},{"name":"B","number":2},{"name":"C","number":3},{"name":"D","number":4}],
		"entries":[[1,2,-3],[1,2],[1],[1,2],[-1,2],[]],
		"completed":[true,true,true,true,true,true]
	}`
	res, err := ComputeODResults(scheme, state)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		number     int64
		place      string
		total      int
		right      int
		wrong      int
		tourTotals []int
	}{
		// A: 3 right +3 bonus, then 1 right 1 wrong = 6 − 2 = 4, best tour 3.
		{1, "1", 4, 4, 1, []int{6, -2}},
		// B: 2 + 2 = 4, the same right answers, but the best tour is 2.
		{2, "2", 4, 4, 0, []int{2, 2}},
		// D answered nothing anywhere: no charge for its empty tours.
		{4, "3", 0, 0, 0, []int{0, 0}},
		// C: one wrong, then an empty tour charged one wrong.
		{3, "4", -6, 0, 2, []int{-3, -3}},
	}
	for i, w := range want {
		got := res.Teams[i]
		if got.Number != w.number || got.Place != w.place || got.Total != w.total || got.Right != w.right || got.Wrong != w.wrong || !slices.Equal(got.TourTotals, w.tourTotals) {
			t.Errorf("row %d = %+v, want %+v", i, got, w)
		}
	}
	// The rating site's mask still marks right answers alone.
	if res.Teams[0].Mask != "111100" {
		t.Errorf("A's mask = %q", res.Teams[0].Mask)
	}
}

func TestComputeODResultsMinusContestedAndUnfinishedTour(t *testing.T) {
	// C's wrong answer on q0 is accepted on appeal: right, not wrong. The second
	// tour is not finished, so B's empty tour there is not charged yet.
	scheme := `{"tourComp":[3,3],"minus":{"wrong":3,"emptyTour":true}}`
	state := `{
		"teams":[{"name":"B","number":2},{"name":"C","number":3}],
		"entries":[[2,-3],[],[],[],[],[]],
		"completed":[true,true,true,true,false,false],
		"contested":[{"question":0,"number":3,"status":"accepted"}]
	}`
	res, err := ComputeODResults(scheme, state)
	if err != nil {
		t.Fatal(err)
	}
	for _, team := range res.Teams {
		if team.Total != 1 || team.Wrong != 0 || team.Right != 1 {
			t.Errorf("team %d: total %d right %d wrong %d, want 1/1/0", team.Number, team.Total, team.Right, team.Wrong)
		}
		if team.Place != "1–2" {
			t.Errorf("team %d place %q, want a shared 1–2", team.Number, team.Place)
		}
	}
}

func TestODMinusSurvivesAClear(t *testing.T) {
	scheme := `{"tourComp":[6,6],"minus":{"wrong":3,"allBonus":3,"emptyTour":true}}`
	shape := od{}.ShapeOf(scheme)
	rebuilt, _, err := od{}.PristineGame("s", "t", shape)
	if err != nil {
		t.Fatal(err)
	}
	if got := ParseODMinus(string(rebuilt)); got == nil || *got != (ODMinus{Wrong: 3, AllBonus: 3, EmptyTour: true}) {
		t.Fatalf("minus after a clear = %+v", got)
	}
}
