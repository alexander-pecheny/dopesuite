// Package matchops applies a match-state PATCH to a match's Protocol state blob
// (ADR-0005). The wire vocabulary is paths, not intent: the server learns which
// blob path an op addresses and calls the typed store.MatchBlob mutator for it,
// so the recorded BlobOps — and therefore journal replay and canonical
// storage — are identical to what any other writer produces. A path outside the
// blob's vocabulary is a shape error; nothing here inspects why the host edited.
package matchops

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"dope/dope/domain/edit"
	"dope/dope/storage/store"
)

// maxShootoutThemes bounds the shootout grid so a bad index can't inflate the
// blob; matches are decided in a handful of extra themes.
const maxShootoutThemes = 32

// Apply replays ops against blob, using match for the slot/roster facts a path
// must resolve against. It returns on the first bad op, having already applied
// the ones before it — callers run it inside a savepoint.
func Apply(blob *store.MatchBlob, match store.DBMatchState, ops []edit.PatchOp) error {
	if len(ops) == 0 {
		return errors.New("missing patch ops")
	}
	for _, op := range ops {
		path, err := edit.ParseJSONPatchPath(op.Path)
		if err != nil {
			return err
		}
		if err := applyOne(blob, match, op, path); err != nil {
			return err
		}
	}
	return nil
}

func applyOne(blob *store.MatchBlob, match store.DBMatchState, op edit.PatchOp, path []edit.JSONPathSegment) error {
	remove := op.Op == "remove"
	if op.Op != "" && op.Op != "set" && !remove {
		return fmt.Errorf("unsupported patch op %q", op.Op)
	}
	if len(path) < 3 || path[0].IsIndex || path[0].Key != "teams" {
		return errors.New("patch path is not a match-state path")
	}
	teamID, slot, err := resolveTeam(match, path[1])
	if err != nil {
		return err
	}

	if !path[2].IsIndex && path[2].Key == "pin" {
		if len(path) != 3 {
			return errors.New("bad pin path")
		}
		if remove {
			blob.SetPin(teamID, nil)
			return nil
		}
		place, err := decodeNumber(op.Value)
		if err != nil || place < 0 {
			return errors.New("bad place")
		}
		blob.SetPin(teamID, &place)
		return nil
	}

	kind, err := themeKind(path[2])
	if err != nil {
		return err
	}
	if len(path) < 4 || !path[3].IsIndex {
		return errors.New("bad theme index")
	}
	themeIndex := path[3].Index
	if err := checkThemeIndex(kind, themeIndex); err != nil {
		return err
	}

	// The theme itself: a set adds it (shootout grids grow), a remove drops it.
	if len(path) == 4 {
		if kind != "shootout" {
			return errors.New("regular themes are fixed")
		}
		if remove {
			blob.RemoveTheme(teamID, kind, themeIndex)
		} else {
			blob.EnsureTheme(teamID, kind, themeIndex)
		}
		return nil
	}

	if path[4].IsIndex {
		return errors.New("bad theme path")
	}
	switch path[4].Key {
	case "player":
		if len(path) != 5 {
			return errors.New("bad player path")
		}
		if remove {
			blob.SetPlayer(teamID, kind, themeIndex, 0)
			return nil
		}
		playerID, err := decodeInt(op.Value)
		if err != nil {
			return errors.New("bad player id")
		}
		if playerID != 0 && !inRoster(match, slot, playerID) {
			return errors.New("player is not in roster")
		}
		blob.SetPlayer(teamID, kind, themeIndex, playerID)
		return nil
	case "answers":
		if len(path) != 6 || !path[5].IsIndex {
			return errors.New("bad answer index")
		}
		if path[5].Index >= len(store.QuestionValues) {
			return errors.New("bad answer index")
		}
		mark := ""
		if !remove {
			if err := json.Unmarshal(op.Value, &mark); err != nil {
				return errors.New("bad mark")
			}
		}
		blob.SetAnswer(teamID, kind, themeIndex, path[5].Index, mark)
		return nil
	}
	return errors.New("patch path is not a match-state path")
}

// resolveTeam maps the path's team segment to a team id that actually occupies
// a slot of this match, returning its slot index for roster lookups.
func resolveTeam(match store.DBMatchState, seg edit.JSONPathSegment) (int64, int, error) {
	if seg.IsIndex {
		return 0, 0, errors.New("team must be addressed by id")
	}
	teamID, err := strconv.ParseInt(seg.Key, 10, 64)
	if err != nil {
		return 0, 0, errors.New("bad team id")
	}
	for slot, id := range match.TeamIDs {
		if id == teamID {
			return teamID, slot, nil
		}
	}
	return 0, 0, errors.New("team is not in this match")
}

func themeKind(seg edit.JSONPathSegment) (string, error) {
	if seg.IsIndex {
		return "", errors.New("patch path is not a match-state path")
	}
	switch seg.Key {
	case "themes":
		return "regular", nil
	case "shootoutThemes":
		return "shootout", nil
	}
	return "", errors.New("patch path is not a match-state path")
}

func checkThemeIndex(kind string, index int) error {
	limit := store.ThemeCount
	if kind == "shootout" {
		limit = maxShootoutThemes
	}
	if index < 0 || index >= limit {
		return errors.New("bad theme index")
	}
	return nil
}

func inRoster(match store.DBMatchState, slot int, playerID int64) bool {
	if slot >= len(match.State.Teams) {
		return false
	}
	for _, member := range match.State.Teams[slot].Roster {
		if member.ID == playerID {
			return true
		}
	}
	return false
}

func decodeNumber(raw json.RawMessage) (float64, error) {
	var value float64
	err := json.Unmarshal(raw, &value)
	return value, err
}

func decodeInt(raw json.RawMessage) (int64, error) {
	var value int64
	err := json.Unmarshal(raw, &value)
	return value, err
}
