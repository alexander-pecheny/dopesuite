// Package matchedit is a Match edit as the domain sees it: a host's ops on a
// bout's Protocol document or a flat Game's, a bout finished or reopened, a
// bout moved to a venue, and then, once for every bout a set of edits
// touched, the result: the bout scored into match_results, its revision and
// journal event, and the Structure's slots resolved. web/editbatch batches
// live edits into windows and broadcasts them; the replay harness calls the
// same functions in its own transactions.
package matchedit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dope/dope/domain/core"
	"dope/dope/domain/edit"
	"dope/dope/domain/flatgame"
	"dope/dope/domain/games"
	"dope/dope/domain/matchops"
	"dope/dope/domain/resolver"
	"dope/dope/domain/scoring"
	"dope/dope/platform/metrics"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// PatchTx applies one editor's ops to a match's Protocol state: EK ops
// address blob paths, every other Protocol's document takes the generic set
// ops, and either way the recorded BlobOps become the journal's record. A
// finished match takes only the edits its format allows after the finish.
func PatchTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, matchID int64, ops []edit.PatchOp) error {
	match, err := loadMatchTx(ctx, tx, scope, matchID)
	if err != nil {
		return err
	}
	if match.State.Finished {
		paths := make([][]json.RawMessage, len(ops))
		for i, op := range ops {
			paths[i] = op.Path
		}
		if !games.EditableWhenFinished(match.GameType, paths) {
			return corei18n.User(dopestrings.Default.Edit.Match.Finished())
		}
	}
	if !store.TeamBlobShaped(match.GameType) {
		next, blobOps, err := ApplyStateOps(match.GameType, match.RawState, ops, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `update matches set state_json = ? where id = ?`, string(next), matchID); err != nil {
			return err
		}
		return festwrite.JournalMatchPatchTx(ctx, tx, matchID, blobOps)
	}
	recorded, err := store.MutateMatchBlobTx(ctx, tx, matchID, func(blob *store.MatchBlob) error {
		return matchops.Apply(blob, match, ops)
	})
	if err != nil {
		return err
	}
	return festwrite.JournalMatchPatchTx(ctx, tx, matchID, recorded)
}

// FinishTx flips a match's finished/active status; SettleTx then turns the
// grid into a result.
func FinishTx(ctx context.Context, tx *sql.Tx, matchID int64, finished bool) error {
	status := "active"
	if finished {
		status = "finished"
	}
	_, err := tx.ExecContext(ctx, `update matches set status = ? where id = ?`, status, matchID)
	return err
}

// SetVenueTx seats a match at the fest's venue of that number.
func SetVenueTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, matchID int64, number int) error {
	var venueID int64
	if err := tx.QueryRowContext(ctx, `
select id from venues where fest_id = ? and number = ?`, scope.FestID, number).Scan(&venueID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("unknown venue")
		}
		return err
	}
	_, err := tx.ExecContext(ctx, `update matches set venue_id = ? where id = ?`, venueID, matchID)
	return err
}

// Change is what a set of edits did to one match: which match, and the
// Structure transition it made, if any. SettleTx fills in Revision.
type Change struct {
	MatchID  int64
	Code     string
	FinishTo *bool
	Venue    int
	Revision int64
}

// SettleTx turns a set of edits into results, once for each match they
// changed: computed places when the match is being finished, the Protocol's
// scorer with any pin over them, the match's and the fest's revision under
// the coarse journal event. Then it resolves the Game's slots once and
// returns the matches whose seats that moved.
func SettleTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, changes []*Change) ([]int64, error) {
	if len(changes) == 0 {
		return nil, nil
	}
	for _, c := range changes {
		eventType, payload := c.event()
		revision, err := scoreTx(ctx, tx, scope, c.MatchID, c.FinishTo != nil && *c.FinishTo, eventType, payload)
		if err != nil {
			return nil, err
		}
		c.Revision = revision
	}
	return resolver.ResolveGameSlotsTx(ctx, tx, scope.GameID)
}

// scoreTx rescores one match after a set of edits and bumps its revision
// under the named journal event.
func scoreTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, matchID int64, finishing bool, eventType, payload string) (int64, error) {
	match, err := loadMatchTx(ctx, tx, scope, matchID)
	if err != nil {
		return 0, err
	}
	if finishing {
		store.AssignComputedPlaces(&match.State)
	}
	if err := scoring.RecalculateMatchResultsTx(ctx, tx, match); err != nil {
		return 0, err
	}
	return bumpMatchRevisionTx(ctx, tx, scope.FestID, matchID, eventType, payload)
}

// event names the coarse live event for a set of edits' effect on one match.
// One that also made a Structure transition records that, since the Protocol
// ops are already journaled per edit as OpMatchPatch records.
func (c *Change) event() (string, string) {
	switch {
	case c.FinishTo != nil:
		return FinishEvent(c.Code, *c.FinishTo)
	case c.Venue != 0:
		return "match:venue", util.MustJSON(map[string]any{"code": c.Code, "venue": c.Venue})
	}
	return "game:state-patch", util.MustJSON(map[string]any{"match": c.Code})
}

// FinishEvent is the journal event of a match finished or reopened.
func FinishEvent(code string, finished bool) (string, string) {
	return "match:update", util.MustJSON(map[string]any{"code": code, "finished": finished})
}

// bumpMatchRevisionTx advances the match's and the fest's revision once for the
// whole window and records the semantic journal event for it.
func bumpMatchRevisionTx(ctx context.Context, tx *sql.Tx, festID, matchID int64, eventType, payload string) (int64, error) {
	if _, err := tx.ExecContext(ctx, `update matches set revision = revision + 1 where id = ?`, matchID); err != nil {
		return 0, err
	}
	return festwrite.BumpFestRevisionTx(ctx, tx, festID, eventType, payload)
}

// ApplyStateOps validates and applies generic set ops to a Protocol state
// document, returning the updated JSON and the semantic journal ops. Shared by
// the flat-game and per-match patch paths so live writes and journal replay
// (the generic branch of storage/journal ApplyMatchPatch) stay one engine.
// sample, when given, records how long the JSON took.
func ApplyStateOps(gameType, stateJSON string, ops []edit.PatchOp, sample *metrics.Sample) ([]byte, []store.BlobOp, error) {
	metricsOn := sample != nil
	if stateJSON == "" {
		stateJSON = "{}"
	}
	var root any
	tUnmarshal := metrics.NowIf(metricsOn)
	if err := json.Unmarshal([]byte(stateJSON), &root); err != nil {
		return nil, nil, fmt.Errorf("stored game state is invalid json: %w", err)
	}
	if metricsOn {
		sample.Unmarshal = time.Since(tUnmarshal)
	}
	if root == nil {
		root = map[string]any{}
	}

	blobOps := make([]store.BlobOp, 0, len(ops))
	for _, op := range ops {
		if op.Op != "" && op.Op != "set" {
			return nil, nil, fmt.Errorf("unsupported patch op %q", op.Op)
		}
		path, err := edit.ParseJSONPatchPath(op.Path)
		if err != nil {
			return nil, nil, err
		}
		if edit.PatchPathTouchesRatingRoster(gameType, path) {
			return nil, nil, edit.ErrRatingRosterImmutable
		}
		value, err := edit.DecodePatchValue(op.Value)
		if err != nil {
			return nil, nil, err
		}
		root, err = edit.ApplyJSONSet(root, path, value)
		if err != nil {
			return nil, nil, err
		}
		blobOps = append(blobOps, store.BlobOp{Kind: "set", Path: pointerFromSegments(path), Value: value, Parts: op.Path})
	}

	tMarshal := metrics.NowIf(metricsOn)
	next, err := json.Marshal(root)
	if err != nil {
		return nil, nil, err
	}
	if metricsOn {
		sample.Marshal = time.Since(tMarshal)
	}
	if err := games.ValidateEdit(gameType, []byte(stateJSON), next); err != nil {
		return nil, nil, err
	}
	return next, blobOps, nil
}

// pointerFromSegments renders a parsed patch path as the JSON pointer an
// OpMatchPatch record carries.
func pointerFromSegments(path []edit.JSONPathSegment) string {
	var b strings.Builder
	for _, seg := range path {
		b.WriteByte('/')
		if seg.IsIndex {
			b.WriteString(strconv.Itoa(seg.Index))
			continue
		}
		escaped := strings.ReplaceAll(seg.Key, "~", "~0")
		b.WriteString(strings.ReplaceAll(escaped, "/", "~1"))
	}
	return b.String()
}

func loadMatchTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, matchID int64) (store.DBMatchState, error) {
	return store.LoadMatchState(ctx, tx, store.MatchSelector{FestID: scope.FestID, GameID: scope.GameID, MatchID: matchID})
}

// PatchGameTx applies one host's ops to a flat Game's document (OD, KSI): read
// it, apply the ops, write it back through flatgame, which seats, scores and
// ranks the Game's one bout, and bump the fest revision under payload. Ops are
// validated and applied before any write, so an error means nothing of this
// edit was written. sample, when given, records the timings.
func PatchGameTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, ops []edit.PatchOp, payload string, sample *metrics.Sample) ([]byte, int64, error) {
	if len(ops) == 0 {
		return nil, 0, errors.New("missing patch ops")
	}
	doc, err := store.LoadGameDoc(ctx, tx, scope.FestID, scope.GameID)
	if err != nil {
		return nil, 0, err
	}
	next, blobOps, err := ApplyStateOps(doc.GameType, doc.State, ops, sample)
	if err != nil {
		return nil, 0, err
	}
	tDB := metrics.NowIf(sample != nil)
	if blobOps == nil {
		blobOps = []store.BlobOp{}
	}
	if err := flatgame.SaveDocumentTx(ctx, tx, scope.FestID, scope.GameID, doc.MatchID, string(next), blobOps); err != nil {
		return nil, 0, err
	}
	revision, err := festwrite.BumpFestRevisionTx(ctx, tx, scope.FestID, "game:state-patch", payload)
	if err != nil {
		return nil, 0, err
	}
	if sample != nil {
		sample.DB = time.Since(tDB)
		sample.Bytes = len(next)
	}
	return next, revision, nil
}
