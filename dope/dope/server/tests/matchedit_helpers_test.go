package tests

import (
	"encoding/json"
	"strconv"
	"testing"

	"dope/dope/domain/edit"
	"dope/dope/storage/store"

	dopeserver "dope/dope/server"
)

// Match edits travel as set-ops against the match's state blob (ADR-0005), so
// tests address teams by id and players by id, exactly as the host editor does.
// These helpers build those ops and drive them through the batcher.

func blobOp(kind string, value any, parts ...any) edit.PatchOp {
	path := make([]json.RawMessage, len(parts))
	for i, part := range parts {
		raw, err := json.Marshal(part)
		if err != nil {
			panic(err)
		}
		path[i] = raw
	}
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return edit.PatchOp{Op: kind, Path: path, Value: raw}
}

func markOp(teamID int64, shootout bool, theme, answer int, mark string) edit.PatchOp {
	kind := "themes"
	if shootout {
		kind = "shootoutThemes"
	}
	return blobOp("set", mark, "teams", teamKey(teamID), kind, theme, "answers", answer)
}

func pinOp(teamID int64, place float64) edit.PatchOp {
	return blobOp("set", place, "teams", teamKey(teamID), "pin")
}

func teamKey(teamID int64) string { return strconv.FormatInt(teamID, 10) }

// matchTeamIDs reads the match's current slot→team-id mapping, which every op
// path needs.
func matchTeamIDs(t *testing.T, srv *dopeserver.Server, scope dopeserver.MatchScope) []int64 {
	t.Helper()
	view, err := srv.LoadScopedMatchViewSnapshot(scope)
	if err != nil {
		t.Fatalf("load match view: %v", err)
	}
	ids := make([]int64, len(view.Teams))
	for i, team := range view.Teams {
		ids[i] = team.ID
	}
	return ids
}

// matchStatePath is the endpoint every Protocol edit PATCHes.
func matchStatePath(festID, gameID int64, code string) string {
	return "/api/fest/" + strconv.FormatInt(festID, 10) +
		"/games/" + strconv.FormatInt(gameID, 10) + "/matches/" + code + "/state"
}

// markBody builds the PATCH body marking one answer cell of the slot-th team,
// resolving the slot to its team id the way the host editor does.
func markBody(t *testing.T, srv *dopeserver.Server, festID int64, code string, slot, theme, answer int, mark string) edit.PatchRequest {
	t.Helper()
	view, err := srv.LoadMatchViewLocked(festID, code)
	if err != nil {
		t.Fatalf("load match view: %v", err)
	}
	return edit.PatchRequest{Ops: []edit.PatchOp{markOp(view.Teams[slot].ID, false, theme, answer, mark)}}
}

// editMark marks one answer cell of the slot-th team, the commonest edit.
func editMark(t *testing.T, srv *dopeserver.Server, scope dopeserver.MatchScope, slot, theme, answer int, mark string) store.MatchView {
	t.Helper()
	ids := matchTeamIDs(t, srv, scope)
	view, err := srv.SubmitMatchEdit(t.Context(), scope, []edit.PatchOp{markOp(ids[slot], false, theme, answer, mark)})
	if err != nil {
		t.Fatalf("mark edit: %v", err)
	}
	return view
}
