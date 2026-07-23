package protocol

import (
	"encoding/json"
	"testing"
)

// Worked example (values 10..50, computed by hand): team A's theme is
// right/wrong/right/-/- = 10-20+30 = 20, with "+" (sum of correct values,
// the EK plus column) = 10+30 = 40; team B's is wrong/-/-/-/right = -10+50
// = 40, plus 50. Places are the host-entered ones.
func TestEKScore(t *testing.T) {
	p, ok := Get("ek")
	if !ok {
		t.Fatal("ek protocol not registered")
	}
	state := `{"teams":[
		{"name":"A","place":2,"themes":[{"player":"P1","answers":["right","wrong","right","",""]}]},
		{"name":"B","place":1,"themes":[{"player":"P2","answers":["wrong","","","","right"]}]}
	]}`
	outcomes, err := p.Score(nil, json.RawMessage(state))
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2", len(outcomes))
	}
	a, b := outcomes[0], outcomes[1]
	if a.Place != 2 || a.Metrics["total"] != 20 || a.Metrics["plus"] != 40 {
		t.Errorf("team A = place %v metrics %v, want place 2 total 20 plus 40", a.Place, a.Metrics)
	}
	if b.Place != 1 || b.Metrics["total"] != 40 || b.Metrics["plus"] != 50 {
		t.Errorf("team B = place %v metrics %v, want place 1 total 40 plus 50", b.Place, b.Metrics)
	}
	if _, ok := Get("nope"); ok {
		t.Fatal("unknown protocol reported as registered")
	}
}

// Worked examples, computed by hand (values 10..50).
// Plain: A takes 10, loses 30, takes 50 = 30; B takes 10 and 50 = 60 → B first.
// Tiebreak: A takes 50 alone (=50), B takes 10+40 (=50); equal totals and
// pluses break on correct counts from the high value down, so A is first.
// Stickers: A's x2 theme scores 2·10−2·30 = −40, its stickerless theme is
// unscored; B's nowrong theme scores 10 (wrong → 0), its emptywrong theme is
// five empties = −150, total −140 → A first. C declined → unplaced.
func TestKSIScore(t *testing.T) {
	p, ok := Get("ksi")
	if !ok {
		t.Fatal("ksi protocol not registered")
	}
	plain, err := p.Score(json.RawMessage(`{"themes":2}`), json.RawMessage(`{
		"participants":[{"number":1,"name":"A"},{"number":2,"name":"B"}],
		"themes":[
			{"answers":[["right","","wrong","","right"],["right","","","","right"]]},
			{"answers":[[],[]]}
		]}`))
	if err != nil {
		t.Fatalf("Score plain: %v", err)
	}
	if plain[0].Place != 2 || plain[0].Metrics["total"] != 30 || plain[0].Metrics["plus"] != 60 {
		t.Errorf("plain A = %+v, want place 2 total 30 plus 60", plain[0])
	}
	if plain[1].Place != 1 || plain[1].Metrics["total"] != 60 || plain[1].Metrics["plus"] != 60 {
		t.Errorf("plain B = %+v, want place 1 total 60 plus 60", plain[1])
	}

	tiebreak, err := p.Score(json.RawMessage(`{"themes":1}`), json.RawMessage(`{
		"participants":[{"number":1,"name":"A"},{"number":2,"name":"B"}],
		"themes":[{"answers":[["","","","","right"],["right","","","right",""]]}]}`))
	if err != nil {
		t.Fatalf("Score tiebreak: %v", err)
	}
	if tiebreak[0].Place != 1 || tiebreak[1].Place != 2 {
		t.Errorf("tiebreak places = %v, %v; want 1, 2 (correct-50 wins)", tiebreak[0].Place, tiebreak[1].Place)
	}

	stickers, err := p.Score(
		json.RawMessage(`{"themes":2,"stickers":{"types":[{"id":"x2"},{"id":"nowrong"},{"id":"emptywrong"}]}}`),
		json.RawMessage(`{
			"participants":[{"number":1,"name":"A"},{"number":2,"name":"B"},{"number":3,"name":"C"}],
			"declined":{"n3":true},
			"stickers":[["x2","nowrong",""],["","emptywrong",""]],
			"themes":[
				{"answers":[["right","","wrong","",""],["right","","wrong","",""],[]]},
				{"answers":[[],["","","","",""],[]]}
			]}`))
	if err != nil {
		t.Fatalf("Score stickers: %v", err)
	}
	if stickers[0].Place != 1 || stickers[0].Metrics["total"] != -40 {
		t.Errorf("stickers A = %+v, want place 1 total -40", stickers[0])
	}
	if stickers[1].Place != 2 || stickers[1].Metrics["total"] != -140 {
		t.Errorf("stickers B = %+v, want place 2 total -140", stickers[1])
	}
	if stickers[2].Place != 0 {
		t.Errorf("declined C place = %v, want 0 (unplaced)", stickers[2].Place)
	}
}

// Worked example, computed by hand. Teams 1,2,3; two completed questions:
// q1 taken by teams 1 and 2, q2 by team 1 alone. Totals 2/1/0. Buchholz-style
// rating (teamCount − takers + 1 per taken question): team 1 gets 2+3=5,
// team 2 gets 2. The tie variant (q2 taken by nobody) makes totals 1/1/0 →
// teams 1 and 2 share place 1, team 3 is third.
func TestODScore(t *testing.T) {
	p, ok := Get("od")
	if !ok {
		t.Fatal("od protocol not registered")
	}
	cfg := json.RawMessage(`{"tourComp":[2]}`)
	teams := `"teams":[
		{"name":"T1","number":1},{"name":"T2","number":2},{"name":"T3","number":3}]`
	outcomes, err := p.Score(cfg, json.RawMessage(`{`+teams+`,
		"entries":[[1,2],[1]],"completed":[true,true]}`))
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	want := []struct {
		place, total, rating float64
	}{{1, 2, 5}, {2, 1, 2}, {3, 0, 0}}
	if len(outcomes) != len(want) {
		t.Fatalf("got %d outcomes, want %d", len(outcomes), len(want))
	}
	for i, w := range want {
		o := outcomes[i]
		if o.Place != w.place || o.Metrics["total"] != w.total || o.Metrics["rating"] != w.rating {
			t.Errorf("team %d = place %v total %v rating %v, want %v/%v/%v",
				i+1, o.Place, o.Metrics["total"], o.Metrics["rating"], w.place, w.total, w.rating)
		}
	}

	tied, err := p.Score(cfg, json.RawMessage(`{`+teams+`,
		"entries":[[1,2],[]],"completed":[true,true]}`))
	if err != nil {
		t.Fatalf("Score (tied): %v", err)
	}
	for i, wantPlace := range []float64{1, 1, 3} {
		if tied[i].Place != wantPlace {
			t.Errorf("tied team %d place = %v, want %v", i+1, tied[i].Place, wantPlace)
		}
	}
}
