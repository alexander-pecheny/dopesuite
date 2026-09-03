package dopeserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/edit"
	"dope/dope/domain/flatgame"
	"dope/dope/domain/protocol"
	"dope/dope/domain/resolver"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	"dope/dope/web/editbatch"
)

// festScope is a local alias for core.FestScope so the existing dopeserver code
// (and leaf packages) name the same type; logic that moved into leaf packages
// uses core.FestScope directly.
type festScope = core.FestScope

// verifyMatchInScope resolves a Match by its code or its letter — a URL says
// BU, the store says s2-g1-m2 — and returns the code, which every scope key
// downstream is built on.
func (s *server) verifyMatchInScope(ctx context.Context, scope festScope, code string) (matchScope, error) {
	row := s.eng.DB.QueryRowContext(ctx, `
select id, code from matches where fest_id = ? and game_id = ? and (code = ? or (letter <> '' and letter = ?))
order by code = ? desc limit 1`,
		scope.FestID, scope.GameID, code, code, code)
	var matchID int64
	if err := row.Scan(&matchID, &code); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return matchScope{}, errMatchNotFound
		}
		return matchScope{}, err
	}
	return matchScope{festScope: scope, MatchID: matchID, Code: code}, nil
}

type matchScope struct {
	festScope
	MatchID int64
	Code    string
}

func matchScopeKey(scope matchScope) string {
	return editbatch.MatchScopeKey(scope.GameID, scope.Code)
}

func festViewScopeKey(scope festScope) string {
	return fmt.Sprintf("fest:%d:%d", scope.FestID, scope.GameID)
}

func (s *server) broadcastFestView(scope festScope, revision int64) {
	s.eng.InvalidateFestViewCache(scope.FestID)
	data, err := s.festViewBytes(scope.FestID, scope.GameID)
	if err != nil {
		return
	}
	s.eng.BroadcastState(scope.FestID, festViewScopeKey(scope), revision, data)
}

// broadcastMatchCascade fans out the views of downstream matches whose slots
// changed when an edit resolved the bracket, so spectators on those matches (or
// the grid) see advancing teams live instead of only on reload.
func (s *server) broadcastMatchCascade(festID, gameID int64, cascaded []store.MatchView) {
	for _, cv := range cascaded {
		data, err := json.Marshal(cv)
		if err != nil {
			continue
		}
		scopeKey := matchScopeKey(matchScope{festScope: festScope{FestID: festID, GameID: gameID}, Code: cv.Code})
		s.eng.BroadcastState(festID, scopeKey, cv.Revision, data)
	}
}

var errMatchNotFound = errors.New("match not found in this game")

type hostPresenceRequest struct {
	Active *bool           `json:"active,omitempty"`
	Cursor json.RawMessage `json:"cursor,omitempty"`
}

type hostPresenceMessage struct {
	UserID    int64           `json:"userID"`
	Username  string          `json:"username"`
	Color     string          `json:"color"`
	Active    bool            `json:"active"`
	Cursor    json.RawMessage `json:"cursor,omitempty"`
	UpdatedAt string          `json:"updatedAt"`
}

func hostPresenceColor(userID int64) string {
	palette := [...]string{
		"#1a73e8",
		"#d93025",
		"#188038",
		"#f29900",
		"#9334e6",
		"#00acc1",
		"#e91e63",
	}
	if userID <= 0 {
		return palette[0]
	}
	return palette[(userID-1)%int64(len(palette))]
}

func (s *server) replaceGameState(reqCtx context.Context, scope festScope, raw []byte) (int64, error) {
	var revision int64
	err := s.eng.WithWriteTx(reqCtx, scope.FestID, "game-state-put", func(ctx context.Context, tx *sql.Tx) error {
		doc, err := store.LoadGameDoc(ctx, tx, scope.FestID, scope.GameID)
		if err != nil {
			return err
		}
		if err := validateImmutableRatingRosterState(doc.GameType, []byte(doc.State), raw); err != nil {
			return err
		}
		if err := flatgame.SaveDocumentTx(ctx, tx, scope.FestID, scope.GameID, doc.MatchID, string(raw), nil); err != nil {
			return err
		}
		var bumpErr error
		revision, bumpErr = festwrite.BumpFestRevisionTx(ctx, tx, scope.FestID, "game:state", string(raw))
		return bumpErr
	})
	return revision, err
}

func validateImmutableRatingRosterState(gameType string, previousRaw, nextRaw []byte) error {
	key, ok := protocol.RatingRosterStateKey(gameType)
	if !ok {
		return nil
	}
	previous, previousOK, err := topLevelCanonicalJSON(previousRaw, key)
	if err != nil {
		return err
	}
	next, nextOK, err := topLevelCanonicalJSON(nextRaw, key)
	if err != nil {
		return err
	}
	if previousOK != nextOK || !bytes.Equal(previous, next) {
		return edit.ErrRatingRosterImmutable
	}
	return nil
}

func topLevelCanonicalJSON(raw []byte, key string) ([]byte, bool, error) {
	if strings.TrimSpace(string(raw)) == "" {
		raw = []byte("{}")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false, err
	}
	value, ok := obj[key]
	if !ok {
		return nil, false, nil
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, false, err
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, false, err
	}
	return canonical, true, nil
}

func (s *server) updateGameScreenSettings(reqCtx context.Context, scope festScope, raw []byte) error {
	return s.eng.WithWriteTx(reqCtx, scope.FestID, "game-screen-settings-put", func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
update games set screen_settings_json = ?, updated_at = ? where fest_id = ? and id = ?`,
			string(raw), util.UtcNow(), scope.FestID, scope.GameID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return sql.ErrNoRows
		}
		_, err = festwrite.BumpFestRevisionTx(ctx, tx, scope.FestID, "game:screen-settings", string(raw))
		return err
	})
}

// loadAllStageMatchViews returns every stage's full match views for the game in
// one pass, stages ordered by position, matches ordered within each. Empty
// stages (e.g. reseed) are omitted.
func (s *server) loadAllStageMatchViews(ctx context.Context, scope festScope) ([]store.StageMatches, error) {
	views, err := s.loadMatchViews(ctx, scope, "")
	if err != nil {
		return nil, err
	}
	out := make([]store.StageMatches, 0)
	byCode := map[string]int{} // stage code -> index in out, preserving order
	for _, view := range views {
		idx, ok := byCode[view.StageCode]
		if !ok {
			idx = len(out)
			byCode[view.StageCode] = idx
			out = append(out, store.StageMatches{Code: view.StageCode})
		}
		out[idx].Matches = append(out[idx].Matches, view)
	}
	return out, nil
}

func (s *server) loadStageMatchViews(ctx context.Context, scope festScope, stageCode string) ([]store.MatchView, error) {
	// An unknown stage code reads as an empty stage: the client renders no tables.
	return s.loadMatchViews(ctx, scope, stageCode)
}

// loadMatchViews reads a game's Matches — one stage's, or all — on ONE read-only
// snapshot, off the write lock: the whole bracket is a consistent point in
// time and a busy editor never stalls this fetch. Four statements however many
// Matches (store.LoadMatchStates).
func (s *server) loadMatchViews(ctx context.Context, scope festScope, stageCode string) ([]store.MatchView, error) {
	tx, err := s.eng.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ctx, cancel := festwrite.BoundedReadContext()
	defer cancel()
	matches, err := store.LoadMatchStates(ctx, tx, store.MatchSelector{FestID: scope.FestID, GameID: scope.GameID, StageCode: stageCode})
	if err != nil {
		return nil, err
	}
	views := make([]store.MatchView, 0, len(matches))
	for _, match := range matches {
		view := store.MatchViewFrom(match)
		view.Seq = s.eng.CurrentStateSeq(matchScopeKey(matchScope{festScope: scope, MatchID: match.MatchID, Code: match.Code}))
		views = append(views, view)
	}
	return views, nil
}

func (s *server) calculateScopedReseed(ctx context.Context, scope festScope, stageCode string) ([]byte, []store.MatchView, int64, error) {
	txCtx, cancel := festwrite.AuditDetachedContext(ctx, scope.FestID)
	defer cancel()
	conn, err := s.eng.AcquireWriteConn(txCtx, "reseed-calc")
	if err != nil {
		return nil, nil, 0, err
	}
	defer conn.Close()

	defer s.eng.LockWrite("reseed-calc")()

	tx, err := s.eng.BeginWriteTxConn(txCtx, conn)
	if err != nil {
		return nil, nil, 0, err
	}
	defer tx.Rollback()

	affected, err := resolver.CalculateReseedStageSlotsTx(txCtx, tx, scope.GameID, stageCode)
	if err != nil {
		return nil, nil, 0, err
	}
	revision, err := festwrite.BumpFestRevisionTx(txCtx, tx, scope.FestID, "reseed:calculate", util.MustJSON(map[string]any{
		"gameID": scope.GameID,
		"stage":  stageCode,
	}))
	if err != nil {
		return nil, nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, 0, err
	}

	view, err := s.loadFestViewLocked(scope.FestID, scope.GameID)
	if err != nil {
		return nil, nil, 0, err
	}
	view.Revision = util.MaxInt64(view.Revision, revision)
	data, err := json.Marshal(view)
	if err != nil {
		return nil, nil, 0, err
	}

	var cascaded []store.MatchView
	for _, mid := range affected {
		cv, err := s.loadMatchViewByIDLocked(scope.FestID, scope.GameID, mid)
		if err != nil || cv.Code == "" {
			continue
		}
		cascaded = append(cascaded, cv)
	}
	return data, cascaded, revision, nil
}
