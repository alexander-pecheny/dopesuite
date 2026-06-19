// Package edit holds the wire DTOs for game-state edits — the JSON-patch-style
// operations the host editor PATCHes and the batcher coalesces, and which the
// journal renders back into human-readable history lines. It is a pure data leaf
// (no server coupling) so the scoped API, the edit batcher, and the page/journal
// rendering layer can all share one definition.
package edit

import "encoding/json"

// PatchRequest is the body of a game-state PATCH: an ordered list of ops.
type PatchRequest struct {
	Ops []PatchOp `json:"ops"`
}

// PatchOp is one JSON-patch-style operation against a game's state document.
type PatchOp struct {
	Op    string            `json:"op,omitempty"`
	Path  []json.RawMessage `json:"path"`
	Value json.RawMessage   `json:"value"`
}
