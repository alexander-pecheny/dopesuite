package journal

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"dope/dope/domain/edit"
	"dope/dope/storage/store"
)

// OpMatchPatch (ADR-0004): the universal semantic edit record for per-match
// Protocol state. One record carries a match id and an ordered list of
// JSON-pointer ops (store.BlobOp) applied to matches.state_json. The payload
// is the same JSON bytes hot and cold (zstd handles the repetition in cold
// segments), so no varint codec or dictionary is involved.

type matchPatchPayload struct {
	Match int64          `json:"m"`
	Ops   []store.BlobOp `json:"ops"`
}

// EncodeMatchPatch renders the payload for an OpMatchPatch journal record.
func EncodeMatchPatch(matchID int64, ops []store.BlobOp) []byte {
	raw, err := json.Marshal(matchPatchPayload{Match: matchID, Ops: ops})
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

// DecodeMatchPatch parses an OpMatchPatch payload.
func DecodeMatchPatch(payload []byte) (int64, []store.BlobOp, error) {
	var p matchPatchPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return 0, nil, err
	}
	return p.Match, p.Ops, nil
}

// ApplyMatchPatch replays one OpMatchPatch record: it loads the match's state
// blob, applies the pointer ops, and stores the result. Paths through missing
// containers are tolerated no-ops (a remove may have preceded), so replay of
// rewritten history never hard-fails mid-stream.
//
// Semantics dispatch on the match's Protocol: EK blobs replay through the
// EK-shaped applier (answers bound, theme padding, team pruning — the
// converter-parity behaviour), everything else replays with the same generic
// JSON-set semantics the live editbatch path uses, so live writes and replays
// stay byte-identical for flat games too.
func ApplyMatchPatch(ctx context.Context, tx interface {
	pkQuerier
	execer
}, payload []byte) error {
	matchID, ops, err := DecodeMatchPatch(payload)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
select m.state_json, coalesce(g.game_type, '')
from matches m left join games g on g.id = m.game_id
where m.id = ?`, matchID)
	if err != nil {
		return err
	}
	var raw, gameType string
	if rows.Next() {
		if err := rows.Scan(&raw, &gameType); err != nil {
			rows.Close()
			return err
		}
	} else {
		rows.Close()
		return nil // match deleted later in history; nothing to patch
	}
	rows.Close()

	var encoded []byte
	if gameType == "" || gameType == "ek" {
		var doc any = map[string]any{}
		if raw != "" && raw != "{}" {
			dec := json.NewDecoder(strings.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&doc); err != nil {
				return fmt.Errorf("match %d blob: %w", matchID, err)
			}
		}
		for _, op := range ops {
			doc = applyBlobOp(doc, op)
		}
		pruneEmptyTeams(doc)
		if encoded, err = json.Marshal(doc); err != nil {
			return err
		}
	} else {
		var doc any
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &doc); err != nil {
				return fmt.Errorf("match %d blob: %w", matchID, err)
			}
		}
		if doc == nil {
			doc = map[string]any{}
		}
		for _, op := range ops {
			doc = applyFlatOp(doc, op)
		}
		if encoded, err = json.Marshal(doc); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `update matches set state_json = ? where id = ?`, string(encoded), matchID)
	return err
}

// applyFlatOp replays one op on a flat game's document. When the record
// carries the client's raw path parts, replay goes through the exact live
// engine (edit.ApplyJSONSet). Records journaled before parts existed only
// have the pointer string, which erased index-vs-key typing for numeric
// segments — there the existing container decides, and a missing one defaults
// to an array (right for everything except a numeric string key set into a
// container the same replay hasn't created yet, e.g. KSI's declined map —
// unrecoverable from the pointer alone). Failures no-op: the live path would
// have rejected the edit before journaling it.
func applyFlatOp(doc any, op store.BlobOp) any {
	switch op.Kind {
	case "replace":
		if op.Value == nil {
			return map[string]any{}
		}
		return op.Value
	case "set":
		if len(op.Parts) > 0 {
			path, err := edit.ParseJSONPatchPath(op.Parts)
			if err != nil {
				return doc
			}
			next, err := edit.ApplyJSONSet(doc, path, op.Value)
			if err != nil {
				return doc
			}
			return next
		}
		return flatSetPath(doc, splitPointer(op.Path), op.Value)
	case "remove":
		return flatRemovePath(doc, splitPointer(op.Path))
	}
	return doc
}

func flatSetPath(node any, parts []string, value any) any {
	if len(parts) == 0 {
		return value
	}
	key := parts[0]
	index, numErr := strconv.Atoi(key)
	switch cur := node.(type) {
	case []any:
		if numErr != nil || index < 0 || index > maxFlatIndex {
			return cur
		}
		for len(cur) <= index {
			cur = append(cur, nil)
		}
		cur[index] = flatSetPath(cur[index], parts[1:], value)
		return cur
	case map[string]any:
		cur[key] = flatSetPath(cur[key], parts[1:], value)
		return cur
	default:
		if numErr == nil && index >= 0 && index <= maxFlatIndex {
			arr := make([]any, index+1)
			arr[index] = flatSetPath(nil, parts[1:], value)
			return arr
		}
		return map[string]any{key: flatSetPath(nil, parts[1:], value)}
	}
}

func flatRemovePath(node any, parts []string) any {
	if len(parts) == 0 {
		return node
	}
	key := parts[0]
	switch cur := node.(type) {
	case []any:
		index, err := strconv.Atoi(key)
		if err != nil || index < 0 || index >= len(cur) {
			return cur
		}
		if len(parts) == 1 {
			return append(cur[:index], cur[index+1:]...)
		}
		cur[index] = flatRemovePath(cur[index], parts[1:])
		return cur
	case map[string]any:
		if len(parts) == 1 {
			delete(cur, key)
			return cur
		}
		if child, exists := cur[key]; exists {
			cur[key] = flatRemovePath(child, parts[1:])
		}
		return cur
	}
	return node
}

// maxFlatIndex mirrors the live path's maxPatchArrayIndex bound.
const maxFlatIndex = 4096

func splitPointer(path string) []string {
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	for i, p := range parts {
		p = strings.ReplaceAll(p, "~1", "/")
		parts[i] = strings.ReplaceAll(p, "~0", "~")
	}
	return parts
}

// applyBlobOp applies one pointer op. Set creates missing containers on the
// way down: object for a named segment, array (padded with empty objects, or
// empty strings inside an "answers" segment) for a numeric one. Remove splices
// arrays and deletes object keys; missing paths no-op.
func applyBlobOp(doc any, op store.BlobOp) any {
	if op.Kind == "replace" {
		if op.Value == nil {
			return map[string]any{}
		}
		return op.Value
	}
	parts := splitPointer(op.Path)
	if len(parts) == 0 {
		return doc
	}
	// Validate before descending so a bad leaf never leaves half-built
	// containers behind: answer indices are bounded by the 5-value scale.
	for i := 1; i < len(parts); i++ {
		if parts[i-1] != "answers" {
			continue
		}
		if index, err := strconv.Atoi(parts[i]); err != nil || index < 0 || index >= 5 {
			return doc
		}
	}
	switch op.Kind {
	case "set":
		return setPath(doc, parts, op.Value, "")
	case "ensure":
		return ensurePath(doc, parts, "")
	case "remove":
		removePath(doc, parts, "")
	}
	return doc
}

// isIndex reports whether a numeric segment addresses an array position given
// the node it lands on: an existing container decides by its own type; a
// missing one falls back to the schema heuristic (directly under "teams" the
// EK blob keys an object by team id, everywhere else numbers index arrays).
func isIndex(node any, key string, parentKey string) (int, bool) {
	index, err := strconv.Atoi(key)
	if err != nil {
		return 0, false
	}
	switch node.(type) {
	case []any:
		return index, true
	case map[string]any:
		return 0, false
	}
	return index, parentKey != "teams"
}

// ensurePath pads containers along the way so the addressed theme exists,
// never overwriting anything that is already there.
func ensurePath(node any, parts []string, parentKey string) any {
	key := parts[0]
	if index, ok := isIndex(node, key, parentKey); ok {
		arr, ok := node.([]any)
		if !ok {
			arr = []any{}
		}
		if index < 0 || index > 10_000 {
			return node
		}
		for len(arr) <= index {
			arr = append(arr, map[string]any{"answers": emptyAnswers()})
		}
		if len(parts) > 1 {
			arr[index] = ensurePath(arr[index], parts[1:], key)
		}
		return arr
	}
	obj, ok := node.(map[string]any)
	if !ok {
		obj = map[string]any{}
	}
	child := obj[key]
	if len(parts) == 1 {
		if child == nil {
			obj[key] = map[string]any{}
		}
		return obj
	}
	if child == nil {
		if _, err := strconv.Atoi(parts[1]); err == nil && key != "teams" {
			child = []any{}
		} else {
			child = map[string]any{}
		}
	}
	obj[key] = ensurePath(child, parts[1:], key)
	return obj
}

// A numeric segment addresses an array index — except directly under "teams",
// where team-id keys are numeric strings inside an object.
func setPath(node any, parts []string, value any, parentKey string) any {
	key := parts[0]
	if index, ok := isIndex(node, key, parentKey); ok {
		inAnswers := parentKey == "answers"
		arr, ok := node.([]any)
		if !ok {
			arr = []any{}
		}
		if index < 0 || index > 10_000 {
			return node
		}
		if inAnswers {
			for len(arr) < 5 {
				arr = append(arr, "")
			}
		}
		for len(arr) <= index {
			if len(parts) == 1 {
				arr = append(arr, map[string]any{})
			} else {
				arr = append(arr, map[string]any{"answers": emptyAnswers()})
			}
		}
		if len(parts) == 1 {
			if inAnswers {
				if s, ok := value.(string); ok {
					arr[index] = s
				}
			} else {
				arr[index] = value
			}
		} else {
			arr[index] = setPath(arr[index], parts[1:], value, key)
		}
		return arr
	}
	obj, ok := node.(map[string]any)
	if !ok {
		obj = map[string]any{}
	}
	if len(parts) == 1 {
		obj[key] = value
		return obj
	}
	child := obj[key]
	if child == nil {
		if _, err := strconv.Atoi(parts[1]); err == nil && key != "teams" {
			child = []any{}
		} else {
			child = map[string]any{}
		}
	}
	obj[key] = setPath(child, parts[1:], value, key)
	return obj
}

func emptyAnswers() []any {
	return []any{"", "", "", "", ""}
}

func removePath(node any, parts []string, parentKey string) any {
	key := parts[0]
	if index, ok := isIndex(node, key, parentKey); ok {
		arr, ok := node.([]any)
		if !ok || index < 0 || index >= len(arr) {
			return node
		}
		if len(parts) == 1 {
			return append(arr[:index], arr[index+1:]...)
		}
		arr[index] = removePath(arr[index], parts[1:], key)
		return arr
	}
	obj, ok := node.(map[string]any)
	if !ok {
		return node
	}
	if len(parts) == 1 {
		delete(obj, key)
		return obj
	}
	if child, exists := obj[key]; exists {
		obj[key] = removePath(child, parts[1:], key)
	}
	return obj
}

// pruneEmptyTeams drops team sections whose themes and shootoutThemes are both
// gone or empty, and the teams container itself when it empties — normalising
// spliced-away history to the converter's shape.
func pruneEmptyTeams(doc any) {
	obj, ok := doc.(map[string]any)
	if !ok {
		return
	}
	teams, ok := obj["teams"].(map[string]any)
	if !ok {
		return
	}
	empty := func(v any) bool {
		arr, ok := v.([]any)
		return v == nil || (ok && len(arr) == 0)
	}
	for key, section := range teams {
		sec, ok := section.(map[string]any)
		if !ok {
			continue
		}
		if empty(sec["themes"]) && empty(sec["shootoutThemes"]) {
			delete(teams, key)
		} else {
			if empty(sec["themes"]) {
				delete(sec, "themes")
			}
			if empty(sec["shootoutThemes"]) {
				delete(sec, "shootoutThemes")
			}
		}
	}
	if len(teams) == 0 {
		delete(obj, "teams")
	}
}
