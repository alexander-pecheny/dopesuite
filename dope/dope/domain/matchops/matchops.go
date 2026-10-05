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
	"strings"

	"dope/dope/domain/edit"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/idstr"
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

// Positions in a match-state path:
// participants/<id>/<kind>/<theme>/<field>/<answer>, or participants/<id>/pin.
const (
	segParticipant = 1
	segKind        = 2
	segTheme       = 3
	segField       = 4
	segAnswer      = 5
)

func applyOne(blob *store.MatchBlob, match store.DBMatchState, op edit.PatchOp, path []edit.JSONPathSegment) error {
	remove := op.Op == "remove"
	if op.Op != "" && op.Op != "set" && !remove {
		return fmt.Errorf("unsupported patch op %q", op.Op)
	}
	// Setting a field to null clears it: a host's undo puts back a pin or a
	// seating that was never there as null.
	if !remove && strings.TrimSpace(string(op.Value)) == "null" {
		remove = true
	}
	// `teams` is the pre-rename spelling (ADR-0007): a browser holding a cached
	// bundle mid-tournament keeps working.
	if len(path) <= segKind || path[0].IsIndex || (path[0].Key != "participants" && path[0].Key != "teams") {
		return errors.New("patch path is not a match-state path")
	}
	participantID, slot, err := resolveParticipant(match, path[segParticipant])
	if err != nil {
		return err
	}
	if !path[segKind].IsIndex && path[segKind].Key == "pin" {
		return applyPin(blob, participantID, op, path, remove)
	}
	kind, err := themeKind(path[segKind])
	if err != nil {
		return err
	}
	if len(path) <= segTheme || !path[segTheme].IsIndex {
		return errors.New("bad theme index")
	}
	themeIndex := path[segTheme].Index
	if err := checkThemeIndex(kind, themeIndex); err != nil {
		return err
	}
	at := themeCell{participantID: participantID, slot: slot, kind: kind, theme: themeIndex}
	// The theme itself: a set adds it (shootout grids grow), a remove drops it.
	if len(path) == segField {
		return applyTheme(blob, at, remove)
	}
	return applyThemeField(blob, match, at, op, path, remove)
}

// themeCell is the theme a path addresses: whose, which grid, which index.
type themeCell struct {
	participantID int64
	slot          int
	kind          string
	theme         int
}

// applyPin sets or clears a Participant's pinned place.
func applyPin(blob *store.MatchBlob, participantID int64, op edit.PatchOp, path []edit.JSONPathSegment, remove bool) error {
	if len(path) != segKind+1 {
		return errors.New("bad pin path")
	}
	if remove {
		blob.SetPin(participantID, nil)
		return nil
	}
	place, err := decodeNumber(op.Value)
	if err != nil || place < 0 {
		return errors.New("bad place")
	}
	blob.SetPin(participantID, &place)
	return nil
}

// applyTheme adds or drops a whole shootout theme; regular themes are fixed.
func applyTheme(blob *store.MatchBlob, at themeCell, remove bool) error {
	if at.kind != "shootout" {
		return errors.New("regular themes are fixed")
	}
	if remove {
		blob.RemoveTheme(at.participantID, at.kind, at.theme)
	} else {
		blob.EnsureTheme(at.participantID, at.kind, at.theme)
	}
	return nil
}

// applyThemeField edits one field of a theme: who sat for it, or one answer.
func applyThemeField(blob *store.MatchBlob, match store.DBMatchState, at themeCell, op edit.PatchOp, path []edit.JSONPathSegment, remove bool) error {
	if path[segField].IsIndex {
		return errors.New("bad theme path")
	}
	switch path[segField].Key {
	// `player` is the pre-Sextet spelling, one id where there is now a list
	// (ADR-0007): a browser holding a cached bundle mid-tournament, and every
	// journal record written before, keeps working.
	case "player", "players":
		if len(path) != segField+1 {
			return errors.New("bad player path")
		}
		return applyPlayers(blob, match, at, path[segField].Key, op, remove)
	case "answers":
		if len(path) != segAnswer+1 || !path[segAnswer].IsIndex {
			return errors.New("bad answer index")
		}
		return applyAnswer(blob, at, path[segAnswer].Index, op, remove)
	}
	return errors.New("patch path is not a match-state path")
}

// applyPlayers seats the players who sat for a theme, or clears them.
func applyPlayers(blob *store.MatchBlob, match store.DBMatchState, at themeCell, key string, op edit.PatchOp, remove bool) error {
	if remove {
		blob.SetPlayers(at.participantID, at.kind, at.theme, nil)
		return nil
	}
	seated, err := decodeSeating(key, op.Value)
	if err != nil {
		return err
	}
	if err := checkSeating(match, at.slot, seated); err != nil {
		return err
	}
	blob.SetPlayers(at.participantID, at.kind, at.theme, seated)
	return nil
}

// applyAnswer marks one answer of a theme, or clears it.
func applyAnswer(blob *store.MatchBlob, at themeCell, answer int, op edit.PatchOp, remove bool) error {
	if answer >= len(store.QuestionValues) {
		return errors.New("bad answer index")
	}
	mark := ""
	if !remove {
		if err := json.Unmarshal(op.Value, &mark); err != nil {
			return errors.New("bad mark")
		}
	}
	blob.SetAnswer(at.participantID, at.kind, at.theme, answer, mark)
	return nil
}

// resolveParticipant maps the path's id segment to a Participant that actually
// occupies a slot of this match, returning its slot index for roster lookups.
func resolveParticipant(match store.DBMatchState, seg edit.JSONPathSegment) (int64, int, error) {
	if seg.IsIndex {
		return 0, 0, errors.New("participant must be addressed by id")
	}
	participantID, err := idstr.Parse(seg.Key)
	if err != nil {
		return 0, 0, errors.New("bad participant id")
	}
	for slot, id := range match.ParticipantIDs {
		if id == participantID {
			return participantID, slot, nil
		}
	}
	return 0, 0, errors.New("participant is not in this match")
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

// decodeSeating reads a seating value in either spelling: a list of player ids
// under `players`, one id under the legacy `player` (0 clearing the theme).
func decodeSeating(key string, raw json.RawMessage) ([]int64, error) {
	if key == "player" {
		playerID, err := decodeInt(raw)
		if err != nil {
			return nil, errors.New("bad player id")
		}
		if playerID == 0 {
			return nil, nil
		}
		return []int64{playerID}, nil
	}
	var seated []int64
	if err := json.Unmarshal(raw, &seated); err != nil {
		return nil, errors.New("bad player ids")
	}
	out := make([]int64, 0, len(seated))
	for _, id := range seated {
		if id != 0 {
			out = append(out, id)
		}
	}
	return out, nil
}

// checkSeating holds a theme's seating to what the game allows: no more
// players than the match seats, nobody twice, nobody who is not in the team's
// roster. All three are things a host can do by hand, so all three read as
// user errors rather than as a broken request.
func checkSeating(match store.DBMatchState, slot int, seated []int64) error {
	s := dopestrings.Default
	cap := match.Players
	if cap <= 0 {
		cap = store.SeatCap(match.GameType)
	}
	if len(seated) > cap {
		return corei18n.User(s.Matchops.Seating.TooMany(cap))
	}
	seen := make(map[int64]bool, len(seated))
	for _, id := range seated {
		if seen[id] {
			return corei18n.User(s.Matchops.Seating.Repeated())
		}
		seen[id] = true
		if !inRoster(match, slot, id) {
			return corei18n.User(s.Matchops.Seating.NotInRoster())
		}
	}
	return nil
}

func inRoster(match store.DBMatchState, slot int, playerID int64) bool {
	if slot >= len(match.State.Participants) {
		return false
	}
	for _, member := range match.State.Participants[slot].Roster {
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
