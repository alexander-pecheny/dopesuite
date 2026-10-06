package pages

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"dope/dope/domain/core"
)

// Commit runs one write to a fest through the engine's commit step
// (core.Engine.CommitFestWrite) and then tells the open pages what changed:
// whatever the write names in its Broadcast, and the fest view of each game
// in views. It returns the revision the write reached. hostpages' commit is
// this one.
func (s *Server) Commit(ctx context.Context, festID int64, label string, views []int64, fn func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error)) (int64, error) {
	var written core.FestWrite
	revision, err := s.h.Engine().CommitFestWrite(ctx, festID, label, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		var err error
		written, err = fn(ctx, tx)
		return written, err
	})
	if err != nil || revision == 0 {
		return revision, err
	}
	b := written.Broadcast
	b.Views = append(slices.Clone(views), b.Views...)
	s.FanOut(festID, revision, b)
	return revision, nil
}

// FanOut tells the open pages what a committed write changed: each game
// document as it is now, each EK Game's roster, and each Game's fest view
// once.
func (s *Server) FanOut(festID, revision int64, b core.Broadcast) {
	eng := s.h.Engine()
	for _, state := range b.States {
		eng.BroadcastState(festID, core.GameStateScope(state.GameID), revision, state.StateJSON)
	}
	for _, gameID := range b.Rosters {
		eng.BroadcastState(festID, fmt.Sprintf("game-roster:%d", gameID), revision, []byte(`{}`))
	}
	seen := make(map[int64]bool, len(b.Views))
	for _, gameID := range b.Views {
		if !seen[gameID] {
			seen[gameID] = true
			s.h.BroadcastFestView(festID, gameID, revision)
		}
	}
}
