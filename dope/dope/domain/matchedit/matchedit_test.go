package matchedit

import "testing"

// present3 is a 3-slot match with every slot filled.
func present3(int) bool { return true }

func intp(v int) *int         { return &v }
func f64p(v float64) *float64 { return &v }
func boolp(v bool) *bool      { return &v }
func strp(v string) *string   { return &v }

func TestValidate_Actions(t *testing.T) {
	p, err := Validate(3, present3, 5, Request{Action: ActionAddShootoutTheme})
	if err != nil || p.Action != ActionAddShootoutTheme {
		t.Fatalf("add shootout: plan=%+v err=%v", p, err)
	}
	if _, err := Validate(3, present3, 5, Request{Action: ActionAddShootoutTheme, HasTeamEdit: true}); err == nil || err.Error() != "action update must be standalone" {
		t.Fatalf("action+team edit should be rejected, got %v", err)
	}
	if _, err := Validate(3, present3, 5, Request{Action: "frobnicate"}); err == nil || err.Error() != "bad action" {
		t.Fatalf("unknown action should be rejected, got %v", err)
	}
}

func TestValidate_TeamBounds(t *testing.T) {
	for _, team := range []int{-1, 3, 99} {
		if _, err := Validate(3, present3, 5, Request{Team: team, Place: f64p(1)}); err == nil || err.Error() != "bad team index" {
			t.Fatalf("team %d should be out of range, got %v", team, err)
		}
	}
	// slot present-check: slot 1 is empty
	teamPresent := func(i int) bool { return i != 1 }
	if _, err := Validate(3, teamPresent, 5, Request{Team: 1, Place: f64p(1)}); err == nil || err.Error() != "bad team index" {
		t.Fatalf("empty slot should be rejected, got %v", err)
	}
}

func TestValidate_Place(t *testing.T) {
	p, err := Validate(3, present3, 5, Request{Team: 0, Place: f64p(2)})
	if err != nil || p.Place == nil || *p.Place != 2 {
		t.Fatalf("valid place: plan=%+v err=%v", p, err)
	}
	if _, err := Validate(3, present3, 5, Request{Team: 0, Place: f64p(-1)}); err == nil || err.Error() != "bad place" {
		t.Fatalf("negative place should be rejected, got %v", err)
	}
	if _, err := Validate(3, present3, 5, Request{Team: 0, Tiebreak: intp(1)}); err == nil || err.Error() != "shootout total is calculated" {
		t.Fatalf("tiebreak should be rejected, got %v", err)
	}
}

func TestValidate_ThemeAndAnswer(t *testing.T) {
	// regular answer edit
	p, err := Validate(3, present3, 5, Request{Team: 0, Theme: intp(1), Answer: intp(2), Mark: strp("+")})
	if err != nil {
		t.Fatalf("valid answer edit err=%v", err)
	}
	if p.Theme == nil || p.Theme.Kind != "regular" || p.Theme.Index != 1 || p.Theme.Answer == nil || p.Theme.Answer.Index != 2 || p.Theme.Answer.Mark != "+" {
		t.Fatalf("unexpected plan %+v", p.Theme)
	}
	// shootout kind
	p, _ = Validate(3, present3, 5, Request{Team: 0, Theme: intp(0), Shootout: boolp(true), Answer: intp(0), Mark: strp("-")})
	if p.Theme.Kind != "shootout" {
		t.Fatalf("expected shootout kind, got %q", p.Theme.Kind)
	}
	// missing/negative theme
	if _, err := Validate(3, present3, 5, Request{Team: 0, Answer: intp(0), Mark: strp("+")}); err == nil || err.Error() != "bad theme index" {
		t.Fatalf("missing theme should be rejected, got %v", err)
	}
	if _, err := Validate(3, present3, 5, Request{Team: 0, Theme: intp(-1), Answer: intp(0), Mark: strp("+")}); err == nil || err.Error() != "bad theme index" {
		t.Fatalf("negative theme should be rejected, got %v", err)
	}
	// answer index bounds against answerValueCount
	if _, err := Validate(3, present3, 5, Request{Team: 0, Theme: intp(0), Answer: intp(5), Mark: strp("+")}); err == nil || err.Error() != "bad answer index" {
		t.Fatalf("out-of-range answer should be rejected, got %v", err)
	}
	// mark required when answer present
	if _, err := Validate(3, present3, 5, Request{Team: 0, Theme: intp(0), Answer: intp(0)}); err == nil || err.Error() != "missing mark" {
		t.Fatalf("missing mark should be rejected, got %v", err)
	}
}

func TestValidate_Player(t *testing.T) {
	// player is trimmed; a real name is carried through
	p, err := Validate(3, present3, 5, Request{Team: 0, Theme: intp(0), Player: strp("  Иванов  ")})
	if err != nil || p.Theme == nil || p.Theme.Player == nil || *p.Theme.Player != "Иванов" {
		t.Fatalf("player trim: plan=%+v err=%v", p.Theme, err)
	}
	// blank player clears (empty string, not nil)
	p, _ = Validate(3, present3, 5, Request{Team: 0, Theme: intp(0), Player: strp("   ")})
	if p.Theme.Player == nil || *p.Theme.Player != "" {
		t.Fatalf("blank player should carry empty string, got %+v", p.Theme.Player)
	}
}
