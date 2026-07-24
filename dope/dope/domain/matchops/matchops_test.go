package matchops

import (
	"encoding/json"
	"testing"

	"dope/dope/domain/edit"
	"dope/dope/storage/store"
)

func testMatch() store.DBMatchState {
	return store.DBMatchState{
		MatchID: 1,
		TeamIDs: []int64{11, 22},
		State: store.MatchState{Teams: []store.TeamState{
			{ID: 11, Roster: []store.RosterMember{{ID: 101, Name: "Анна Б."}}},
			{ID: 22, Roster: []store.RosterMember{{ID: 202, Name: "Пётр В."}}},
		}},
	}
}

func op(kind string, value string, parts ...any) edit.PatchOp {
	path := make([]json.RawMessage, len(parts))
	for i, part := range parts {
		path[i], _ = json.Marshal(part)
	}
	return edit.PatchOp{Op: kind, Path: path, Value: json.RawMessage(value)}
}

// The wire vocabulary is blob paths: a mark, a player id and a pin each land on
// their typed mutator, and the recorded ops are the canonical ones.
func TestApplyPaths(t *testing.T) {
	blob := store.MatchBlob{}
	err := Apply(&blob, testMatch(), []edit.PatchOp{
		op("set", `"right"`, "teams", "11", "themes", 2, "answers", 4),
		op("set", `101`, "teams", "11", "themes", 2, "player"),
		op("set", `1.5`, "teams", "22", "pin"),
		op("set", `{}`, "teams", "22", "shootoutThemes", 0),
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := blob.Teams["11"].Themes[2].Answers[4]; got != "right" {
		t.Fatalf("answer = %q", got)
	}
	if got := blob.Teams["11"].Themes[2].Player; got != 101 {
		t.Fatalf("player = %d", got)
	}
	if got := blob.Pin(22); got == nil || *got != 1.5 {
		t.Fatalf("pin = %v", got)
	}
	if len(blob.Teams["22"].ShootoutThemes) != 1 {
		t.Fatalf("shootout themes = %d", len(blob.Teams["22"].ShootoutThemes))
	}
	if len(blob.Ops) != 4 {
		t.Fatalf("recorded %d ops, want 4", len(blob.Ops))
	}
}

// A remove clears rather than deletes where the blob's shape is fixed, and
// splices where it grows.
func TestApplyRemove(t *testing.T) {
	blob := store.MatchBlob{}
	place := 2.0
	blob.SetPin(11, &place)
	blob.SetPlayer(11, "regular", 0, 101)
	blob.EnsureTheme(11, "shootout", 0)
	if err := Apply(&blob, testMatch(), []edit.PatchOp{
		op("remove", ``, "teams", "11", "pin"),
		op("remove", ``, "teams", "11", "themes", 0, "player"),
		op("remove", ``, "teams", "11", "shootoutThemes", 0),
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if blob.Pin(11) != nil || blob.Teams["11"].Themes[0].Player != 0 {
		t.Fatalf("removes did not clear: %+v", blob.Teams["11"])
	}
	if len(blob.Teams["11"].ShootoutThemes) != 0 {
		t.Fatal("shootout theme should be spliced out")
	}
}

// Anything outside the blob's vocabulary is a shape error. Intent is never
// inspected — only whether the path can exist and what it may hold.
func TestApplyRejects(t *testing.T) {
	cases := []struct {
		name string
		op   edit.PatchOp
	}{
		{"foreign team", op("set", `"right"`, "teams", "99", "themes", 0, "answers", 0)},
		{"team by slot", op("set", `"right"`, "teams", 0, "themes", 0, "answers", 0)},
		{"not a match path", op("set", `1`, "questions", 0)},
		{"answer out of scale", op("set", `"right"`, "teams", "11", "themes", 0, "answers", 9)},
		{"theme out of range", op("set", `"right"`, "teams", "11", "themes", 99, "answers", 0)},
		{"regular theme added", op("set", `{}`, "teams", "11", "themes", 3)},
		{"negative pin", op("set", `-1`, "teams", "11", "pin")},
		{"player off roster", op("set", `999`, "teams", "11", "themes", 0, "player")},
		{"unknown leaf", op("set", `1`, "teams", "11", "themes", 0, "colour")},
		{"unsupported op", op("add", `1`, "teams", "11", "pin")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blob := store.MatchBlob{}
			if err := Apply(&blob, testMatch(), []edit.PatchOp{tc.op}); err == nil {
				t.Fatal("expected a shape error")
			}
		})
	}
}
