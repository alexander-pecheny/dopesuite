package store

import "testing"

func TestScoreTeam(t *testing.T) {
	// One regular theme: right on 10 and 30, wrong on 20 → total 10-20+30 = 20,
	// plus = 40, correct counts at idx0 and idx2, wrong count at idx1.
	team := TeamState{
		Name: "A",
		Themes: []ThemeEntry{
			{Player: "p1", Answers: [5]string{"right", "wrong", "right", "", ""}},
		},
		ShootoutThemes: []ThemeEntry{
			{Player: "p1", Answers: [5]string{"right", "", "", "", ""}}, // +10 shootout
		},
	}
	tv := ScoreTeam(team)
	if tv.Total != 20 {
		t.Errorf("Total = %d, want 20", tv.Total)
	}
	if tv.Plus != 40 {
		t.Errorf("Plus = %d, want 40", tv.Plus)
	}
	if tv.CorrectCounts != [5]int{1, 0, 1, 0, 0} {
		t.Errorf("CorrectCounts = %v", tv.CorrectCounts)
	}
	if tv.WrongCounts != [5]int{0, 1, 0, 0, 0} {
		t.Errorf("WrongCounts = %v", tv.WrongCounts)
	}
	if tv.ShootoutTotal != 10 || tv.Tiebreak != 10 {
		t.Errorf("ShootoutTotal/Tiebreak = %d/%d, want 10/10", tv.ShootoutTotal, tv.Tiebreak)
	}
	if tv.Themes[0].Score != 20 {
		t.Errorf("theme score = %d, want 20", tv.Themes[0].Score)
	}
}

func TestBuildViewStandings(t *testing.T) {
	state := MatchState{
		Title: "M",
		Teams: []TeamState{
			{Name: "A", Place: 2, Themes: []ThemeEntry{{Answers: [5]string{"right", "", "", "", ""}}}},
			{Name: "B", Place: 1, Themes: []ThemeEntry{{Answers: [5]string{"", "", "", "", "right"}}}},
		},
	}
	view := BuildView(state)
	// Standings: placed teams sorted by place → B(1) before A(2).
	if len(view.Standings) != 2 || view.Standings[0].Name != "B" || view.Standings[1].Name != "A" {
		t.Fatalf("standings order = %+v", view.Standings)
	}
	// Places propagate back onto the team views.
	for _, tm := range view.Teams {
		if tm.Name == "B" && tm.Place != 1 {
			t.Errorf("B place = %v, want 1", tm.Place)
		}
	}
}
