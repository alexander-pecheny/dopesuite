package matchops

import (
	"errors"
	"testing"

	"dope/dope/domain/edit"
	"dope/dope/storage/store"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// sextetMatch is a бой of Эрудит-Секстет: three seats per theme and a team of
// four to fill them from.
func sextetMatch() store.DBMatchState {
	match := store.DBMatchState{
		MatchID:        1,
		GameType:       "es",
		Players:        3,
		ParticipantIDs: []int64{11},
		State: store.MatchState{Participants: []store.ParticipantState{
			{ID: 11, Roster: []store.RosterMember{
				{ID: 101, Name: "Анна Б."},
				{ID: 102, Name: "Пётр В."},
				{ID: 103, Name: "Ольга Г."},
				{ID: 104, Name: "Иван Д."},
			}},
		}},
	}
	return match
}

// A seating arrives as the whole list and is stored as one.
func TestApplySeating(t *testing.T) {
	blob := store.MatchBlob{}
	if err := Apply(&blob, sextetMatch(), []edit.PatchOp{
		op("set", `[101,103]`, "participants", "11", "themes", 0, "players"),
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	seated := blob.Participants["11"].Themes[0].Players
	if len(seated) != 2 || seated[0] != 101 || seated[1] != 103 {
		t.Fatalf("seated = %v, want [101 103]", seated)
	}
	if got := blob.Ops[0].Path; got != "/participants/11/themes/0/players" {
		t.Fatalf("op path = %s", got)
	}
	// Emptying the list clears the theme rather than seating nobody by name.
	if err := Apply(&blob, sextetMatch(), []edit.PatchOp{
		op("set", `[]`, "participants", "11", "themes", 0, "players"),
	}); err != nil {
		t.Fatalf("apply empty: %v", err)
	}
	if got := blob.Participants["11"].Themes[0].Players; len(got) != 0 {
		t.Fatalf("cleared seating = %v", got)
	}
}

// The three ways a host can seat a theme wrongly are all things a host can do,
// so all three come back as user errors the edge shows verbatim — never a 500.
func TestSeatingViolationsAreUserErrors(t *testing.T) {
	cases := []struct{ name, value string }{
		{"over the cap", `[101,102,103,104]`},
		{"the same player twice", `[101,101]`},
		{"a player outside the roster", `[101,999]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blob := store.MatchBlob{}
			err := Apply(&blob, sextetMatch(), []edit.PatchOp{
				op("set", c.value, "participants", "11", "themes", 0, "players"),
			})
			if err == nil {
				t.Fatal("accepted")
			}
			var user corei18n.UserError
			if !errors.As(err, &user) {
				t.Fatalf("err = %v (%T), want a UserError", err, err)
			}
		})
	}
}

// ЭК seats one player, and the same check holds a client that tries for two.
func TestQuartetSeatsOnePlayer(t *testing.T) {
	match := sextetMatch()
	match.GameType = "ek"
	match.Players = 1
	blob := store.MatchBlob{}
	if err := Apply(&blob, match, []edit.PatchOp{
		op("set", `[101,102]`, "participants", "11", "themes", 0, "players"),
	}); err == nil {
		t.Fatal("ЭК accepted two players on a theme")
	}
}

// The singular `player` is what a browser holding a pre-Секстет bundle sends
// and what every journal record written before it carries; it lands in the
// list, so nothing has to be rewritten to keep working.
func TestLegacySinglePlayerOp(t *testing.T) {
	blob := store.MatchBlob{}
	if err := Apply(&blob, sextetMatch(), []edit.PatchOp{
		op("set", `101`, "participants", "11", "themes", 0, "player"),
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := blob.Participants["11"].Themes[0].Players; len(got) != 1 || got[0] != 101 {
		t.Fatalf("seated = %v, want [101]", got)
	}
	// A write always records the new spelling, whatever the op said.
	if got := blob.Ops[0].Path; got != "/participants/11/themes/0/players" {
		t.Fatalf("op path = %s", got)
	}
}
