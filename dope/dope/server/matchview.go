package dopeserver

import (
	"context"
	"database/sql"
	"dope/dope/domain/resolver"
	"dope/dope/domain/scoring"
	"dope/dope/platform/metrics"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"
)

// Venue-edit request bodies used by the match-update handlers below.
type venueUpdateRequest struct {
	Title string `json:"title"`
}

type matchVenueRequest struct {
	Number      int `json:"number"`
	VenueNumber int `json:"venueNumber"`
}

func (s *server) loadFestViewLocked(festID, gameID int64) (store.FestView, error) {
	if s.eng.DB == nil {
		match := store.BuildView(s.eng.State)
		return store.FestView{
			Slug:              "legacy",
			Title:             match.Title,
			Revision:          match.Revision,
			UpdatedAt:         match.UpdatedAt,
			QuestionValues:    store.QuestionValues,
			RegularThemeCount: store.ThemeCount,
		}, nil
	}
	return s.loadFestViewUsing(s.eng.DB, festID, gameID)
}

// loadFestViewSnapshot builds the fest view on a read-only transaction WITHOUT
// taking the global write mutex. WAL gives the read a consistent snapshot even
// while a writer holds s.eng.Mu, so cross-game page loads / bracket fetches no longer
// queue behind a busy editor — Go's RWMutex is writer-preferring, so a single
// pending writer otherwise blocks every RLock reader. Falls back to the locked
// path in legacy (no-DB) mode.
func (s *server) loadFestViewSnapshot(festID, gameID int64) (store.FestView, error) {
	if s.eng.DB == nil {
		return s.loadFestViewLocked(festID, gameID)
	}
	tx, err := s.eng.DB.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return store.FestView{}, err
	}
	defer tx.Rollback()
	return s.loadFestViewUsing(tx, festID, gameID)
}

// loadFestViewUsing runs every fest-view query against the given queryer, so the
// snapshot path can pass a read-only tx (one WAL snapshot, off the write lock)
// and the locked path can pass s.eng.DB while holding s.eng.Mu.
func (s *server) loadFestViewUsing(q store.Queryer, festID, gameID int64) (store.FestView, error) {
	ctx, cancel := festwrite.BoundedReadContext()
	defer cancel()
	var view store.FestView
	view.QuestionValues = store.QuestionValues
	view.RegularThemeCount = store.ThemeCount
	if festID == 0 {
		view.Slug = ""
		view.Title = ""
		view.UpdatedAt = ""
		return view, nil
	}
	var updatedAt string
	if err := q.QueryRowContext(ctx, `
select coalesce(t.slug, ''), t.title, t.revision, t.updated_at, coalesce(g.scheme_json, ''), coalesce(g.title, '')
from fests t
left join games g on g.fest_id = t.id and g.id = ?
where t.id = ?`, gameID, festID).
		Scan(&view.Slug, &view.Title, &view.Revision, &updatedAt, &view.SchemaJSON, &view.GameName); err != nil {
		return store.FestView{}, err
	}
	view.UpdatedAt = updatedAt

	venues, err := store.LoadVenues(ctx, q, festID)
	if err != nil {
		return store.FestView{}, err
	}
	view.Venues = venues

	stageWhere := "fest_id = ?"
	stageArgs := []any{festID}
	if gameID > 0 {
		stageWhere += " and game_id = ?"
		stageArgs = append(stageArgs, gameID)
	}
	stageRows, err := q.QueryContext(ctx, `
select id, code, title, stage_type, position, status, config_json
from stages
where `+stageWhere+`
order by position, id`, stageArgs...)
	if err != nil {
		return store.FestView{}, err
	}
	defer stageRows.Close()

	type stageRecord struct {
		ID    int64
		Stage store.StageView
	}
	var stageRecords []stageRecord
	for stageRows.Next() {
		var stageID int64
		var stage store.StageView
		var configJSON string
		if err := stageRows.Scan(&stageID, &stage.Code, &stage.Title, &stage.Type, &stage.Position, &stage.Status, &configJSON); err != nil {
			return store.FestView{}, err
		}
		stage.Config = json.RawMessage(store.NonEmptyJSON(configJSON))
		stageRecords = append(stageRecords, stageRecord{ID: stageID, Stage: stage})
	}
	if err := stageRows.Err(); err != nil {
		return store.FestView{}, err
	}
	if err := stageRows.Close(); err != nil {
		return store.FestView{}, err
	}
	for _, record := range stageRecords {
		if record.Stage.Type == "reseed" {
			entries, err := store.LoadReseedEntries(ctx, q, record.ID)
			if err != nil {
				return store.FestView{}, err
			}
			record.Stage.ReseedEntries = entries
			state, err := resolver.ReseedPrerequisites(ctx, q, record.Stage.Config, gameID)
			if err != nil {
				return store.FestView{}, err
			}
			record.Stage.ReseedReady = state.Ready
			record.Stage.ReseedPending = state.PendingMatches
			if !state.Ready {
				record.Stage.ReseedMessage = resolver.ReseedNotReadyMessage(state.PendingMatches)
			}
		} else {
			matches, err := store.LoadFestMatches(ctx, q, record.ID)
			if err != nil {
				return store.FestView{}, err
			}
			record.Stage.Matches = matches
		}
		view.Stages = append(view.Stages, record.Stage)
	}
	return view, nil
}

func (s *server) loadVenuesLocked(festID int64) ([]store.VenueView, error) {
	ctx, cancel := festwrite.BoundedReadContext()
	defer cancel()
	return store.LoadVenues(ctx, s.eng.DB, festID)
}

func (s *server) loadMatchViewLocked(festID int64, code string) (store.MatchView, error) {
	if s.eng.DB == nil {
		return store.BuildView(s.eng.State), nil
	}
	ctx, cancel := festwrite.BoundedReadContext()
	defer cancel()
	match, err := store.LoadDBMatchState(ctx, s.eng.DB, festID, code)
	if err != nil {
		return store.MatchView{}, err
	}
	return store.MatchViewFrom(match), nil
}

func (s *server) loadScopedMatchViewLocked(scope matchScope) (store.MatchView, error) {
	if s.eng.DB == nil {
		return store.BuildView(s.eng.State), nil
	}
	return s.loadScopedMatchViewUsing(s.eng.DB, scope)
}

// loadScopedMatchViewUsing reads a match view via the given queryer, so callers
// can supply a read-only snapshot tx (off the write lock) or s.eng.DB under s.eng.Mu.
func (s *server) loadScopedMatchViewUsing(q store.Queryer, scope matchScope) (store.MatchView, error) {
	ctx, cancel := festwrite.BoundedReadContext()
	defer cancel()
	match, err := loadDBMatchStateByScope(ctx, q, scope)
	if err != nil {
		return store.MatchView{}, err
	}
	return store.MatchViewFrom(match), nil
}

// loadScopedMatchViewSnapshot reads a match view on a read-only transaction
// WITHOUT the global write mutex (see loadFestViewSnapshot) — so a viewer/host
// reading match B doesn't stall behind an editor writing match A.
func (s *server) loadScopedMatchViewSnapshot(scope matchScope) (store.MatchView, error) {
	if s.eng.DB == nil {
		return s.loadScopedMatchViewLocked(scope)
	}
	tx, err := s.eng.DB.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return store.MatchView{}, err
	}
	defer tx.Rollback()
	return s.loadScopedMatchViewUsing(tx, scope)
}

func loadDBMatchStateByScope(ctx context.Context, q store.Queryer, scope matchScope) (store.DBMatchState, error) {
	return store.LoadDBMatchStateWhere(ctx, q, `m.id = ? and m.fest_id = ? and m.game_id = ?`, scope.MatchID, scope.FestID, scope.GameID)
}

// loadMatchViewByIDLocked loads a match view by its numeric id (used to render
// downstream matches touched by a bracket cascade). Caller holds s.eng.Mu.
func (s *server) loadMatchViewByIDLocked(festID, gameID, matchID int64) (store.MatchView, error) {
	if s.eng.DB == nil {
		return store.MatchView{}, nil
	}
	ctx, cancel := festwrite.BoundedReadContext()
	defer cancel()
	match, err := store.LoadDBMatchStateWhere(ctx, s.eng.DB, `m.id = ? and m.fest_id = ? and m.game_id = ?`, matchID, festID, gameID)
	if err != nil {
		return store.MatchView{}, err
	}
	return store.MatchViewFrom(match), nil
}

func recalculateMatchResultsTx(ctx context.Context, tx *sql.Tx, festID int64, code string) error {
	match, err := store.LoadDBMatchState(ctx, tx, festID, code)
	if err != nil {
		return err
	}
	return scoring.RecalculateMatchResultsTx(ctx, tx, match)
}

func bumpMatchRevisionTx(ctx context.Context, tx *sql.Tx, festID, matchID int64, eventType, payload string) (int64, error) {
	now := util.UtcNow()
	if _, err := tx.ExecContext(ctx, `update matches set revision = revision + 1 where id = ?`, matchID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `update fests set revision = revision + 1, updated_at = ? where id = ?`, now, festID); err != nil {
		return 0, err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `select revision from fests where id = ?`, festID).Scan(&revision); err != nil {
		return 0, err
	}
	if err := festwrite.AppendJournalTx(ctx, tx, festID, revision, eventType, []byte(payload)); err != nil {
		return 0, err
	}
	return revision, nil
}

func (s *server) updateVenue(reqCtx context.Context, festID int64, number int, title string) ([]store.VenueView, int64, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, 0, errors.New("empty venue title")
	}

	ctx, cancel := festwrite.AuditDetachedContext(reqCtx, festID)
	defer cancel()
	conn, err := s.eng.AcquireWriteConn(ctx, "venue-rename")
	if err != nil {
		return nil, 0, err
	}
	defer conn.Close()

	defer s.eng.LockWrite("venue-rename")()

	tx, err := s.eng.BeginWriteTxConn(ctx, conn)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
update venues set title = ?, updated_at = ?
where fest_id = ? and number = ?`, title, util.UtcNow(), festID, number)
	if err != nil {
		return nil, 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, 0, err
	}
	if affected == 0 {
		return nil, 0, errors.New("unknown venue")
	}
	revision, err := festwrite.BumpFestRevisionTx(ctx, tx, festID, "venues:update", util.MustJSON(map[string]any{"number": number, "title": title}))
	if err != nil {
		return nil, 0, err
	}
	venues, err := store.LoadVenues(ctx, tx, festID)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return venues, revision, nil
}

// broadcastMatchView fans out a match-scope update as a minimal delta when ops
// are available (cheaper than the full view) and as a full-state snapshot
// otherwise. Centralizes the delta-or-snapshot choice for the match handlers.
// Returns the seq assigned to the broadcast so the handler can stamp it on the
// HTTP response.
func (s *server) broadcastMatchView(festID int64, mscope matchScope, revision int64, deltaOps, data []byte) uint64 {
	scope := matchScopeKey(mscope)
	if len(deltaOps) > 0 {
		return s.eng.BroadcastStateDelta(festID, scope, revision, deltaOps)
	}
	return s.eng.BroadcastState(festID, scope, revision, data)
}

func (s *server) cachedFestViewBytes(festID, gameID int64) ([]byte, bool) {
	s.eng.FestViewMu.RLock()
	defer s.eng.FestViewMu.RUnlock()
	games, ok := s.eng.FestViewCache[festID]
	if !ok {
		return nil, false
	}
	data, ok := games[gameID]
	return data, ok
}

func (s *server) storeFestViewBytes(festID, gameID int64, data []byte) {
	s.eng.FestViewMu.Lock()
	defer s.eng.FestViewMu.Unlock()
	if s.eng.FestViewCache == nil {
		s.eng.FestViewCache = map[int64]map[int64][]byte{}
	}
	games := s.eng.FestViewCache[festID]
	if games == nil {
		games = map[int64][]byte{}
		s.eng.FestViewCache[festID] = games
	}
	games[gameID] = data
}

// festViewBytes returns the JSON-marshaled FestView for (festID, gameID),
// using the in-memory cache when fresh. Cache misses run the same DB queries
// as the original handler and store the result. Invalidation is driven by
// broadcastState.
func (s *server) festViewBytes(festID, gameID int64) ([]byte, error) {
	if data, ok := s.cachedFestViewBytes(festID, gameID); ok {
		if s.metrics.On {
			s.metrics.FestViewHits.Add(1)
		}
		return data, nil
	}
	// Cache miss: rebuild from the DB. Edits invalidate the whole fest's cache
	// (see invalidateFestViewCache), so under concurrent editing this rebuild can
	// fire on every reader request — the suspected amplification. Time it so the
	// live test shows whether it's actually costly.
	tRebuild := metrics.NowIf(s.metrics.On)
	view, err := s.loadFestViewSnapshot(festID, gameID)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	s.storeFestViewBytes(festID, gameID, data)
	if s.metrics.On {
		s.metrics.FestViewMisses.Add(1)
		log.Printf("editmetric festview fest=%d game=%d rebuild_ms=%s bytes=%d",
			festID, gameID, metrics.FmtMs(time.Since(tRebuild)), len(data))
	}
	return data, nil
}
