package dopeserver

import (
	"context"
	"database/sql"
	"dope/dope/domain/matchedit"
	"dope/dope/domain/resolver"
	"dope/dope/platform/metrics"
	"dope/dope/platform/realtime"
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
	return matchViewFromDBState(match), nil
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
	return matchViewFromDBState(match), nil
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

func matchViewFromDBState(match store.DBMatchState) store.MatchView {
	view := store.BuildView(match.State)
	view.Code = match.Code
	view.StageCode = match.StageCode
	view.StageTitle = match.StageTitle
	view.Venue = match.Venue
	return view
}

func (s *server) applyMatchUpdate(festID int64, code string, req updateRequest) (store.MatchView, []byte, error) {
	if s.eng.DB == nil {
		return s.applyLegacyUpdate(req)
	}
	// Legacy single-fest path: no SSE fan-out, so cascade + delta are discarded.
	view, data, _, err := s.applyMatchUpdateUsing(context.Background(), festID, []updateRequest{req},
		func(ctx context.Context, q store.Queryer) (store.DBMatchState, error) {
			return store.LoadDBMatchState(ctx, q, festID, code)
		},
		func() (store.MatchView, error) {
			return s.loadMatchViewLocked(festID, code)
		}, nil)
	return view, data, err
}

// applyScopedMatchUpdate applies a match edit and additionally returns deltaOps:
// the set-ops that turn the pre-edit view into the new one, when broadcasting
// them is cheaper than the full view (else nil — caller broadcasts full state).
func (s *server) applyScopedMatchUpdate(ctx context.Context, scope matchScope, reqs []updateRequest) (store.MatchView, []byte, []byte, []store.MatchView, error) {
	if s.eng.DB == nil {
		var view store.MatchView
		var data []byte
		var err error
		for _, req := range reqs {
			if view, data, err = s.applyLegacyUpdate(req); err != nil {
				return view, data, nil, nil, err
			}
		}
		return view, data, nil, nil, err
	}
	var oldData []byte
	view, data, cascaded, err := s.applyMatchUpdateUsing(ctx, scope.FestID, reqs,
		func(ctx context.Context, q store.Queryer) (store.DBMatchState, error) {
			return loadDBMatchStateByScope(ctx, q, scope)
		},
		func() (store.MatchView, error) {
			return s.loadScopedMatchViewLocked(scope)
		}, &oldData)
	if err != nil {
		return view, data, nil, cascaded, err
	}
	deltaOps, _ := realtime.MatchDeltaOps(oldData, data)
	return view, data, deltaOps, cascaded, nil
}

// applyMatchUpdateUsing applies one match edit and returns the edited match's
// view plus `cascaded`: the views of any OTHER matches whose slots changed when
// the edit resolved the bracket (e.g. finishing a bout advances teams into the
// next round). The handler broadcasts those too, so spectators see downstream
// matches update live instead of only on reload.
func (s *server) applyMatchUpdateUsing(
	reqCtx context.Context,
	festID int64,
	reqs []updateRequest,
	loadMatch func(context.Context, store.Queryer) (store.DBMatchState, error),
	loadView func() (store.MatchView, error),
	oldDataOut *[]byte,
) (store.MatchView, []byte, []store.MatchView, error) {
	// Carry the request's audit attribution (actor/request/fest) into the write
	// tx, detached from the request's cancellation, so this match edit is
	// recorded in audit_log against the right user and fest. The ctx carries a
	// festwrite.WriteTxTimeout so the write can never hold s.eng.Mu indefinitely.
	ctx, cancel := festwrite.AuditDetachedContext(reqCtx, festID)
	defer cancel()
	// Acquire the pooled connection BEFORE taking the lock: the pool wait is the
	// one step that can block unbounded under connection starvation, so keep it
	// off s.eng.Mu (see the 2026-06-13 site-wide freeze). ctx bounds the wait.
	conn, err := s.eng.AcquireWriteConn(ctx, "match-update")
	if err != nil {
		return store.MatchView{}, nil, nil, err
	}
	defer conn.Close()

	defer s.eng.LockWrite("match-update")()

	// Capture the committed pre-image of this match under our exclusive lock,
	// before the mutation commits, so the caller can broadcast a minimal delta
	// against it. Atomic with the mutation (same lock hold) — no TOCTOU window.
	// Best-effort: on any failure oldDataOut stays empty and the caller falls
	// back to a full-state broadcast.
	if oldDataOut != nil {
		if oldView, oerr := loadView(); oerr == nil {
			*oldDataOut, _ = json.Marshal(oldView)
		}
	}

	tx, err := s.eng.BeginWriteTxConn(ctx, conn)
	if err != nil {
		return store.MatchView{}, nil, nil, err
	}
	defer tx.Rollback()

	match, err := loadMatch(ctx, tx)
	if err != nil {
		return store.MatchView{}, nil, nil, err
	}

	dataEdited := false
	for _, req := range reqs {
		if req.Finished != nil {
			if len(reqs) > 1 || hasMatchEdit(req) {
				return store.MatchView{}, nil, nil, errors.New("finished update must be standalone")
			}
			status := "active"
			if *req.Finished {
				status = "finished"
			}
			if _, err := tx.ExecContext(ctx, `update matches set status = ? where id = ?`, status, match.MatchID); err != nil {
				return store.MatchView{}, nil, nil, err
			}
			if *req.Finished {
				assignComputedPlaces(&match.State)
			}
			continue
		}
		if match.State.Finished {
			return store.MatchView{}, nil, nil, errors.New("match is finished")
		}
		if err := applyMatchEditTx(ctx, tx, match, req); err != nil {
			return store.MatchView{}, nil, nil, err
		}
		dataEdited = true
	}

	// applyMatchEditTx writes answer/theme rows, not the in-memory match.State,
	// so reload before recalc/slot resolution to compute results from the edited
	// rows — otherwise a batch would persist results that lag every edit. The
	// finished branch instead computes places into match.State in memory and
	// relies on the recalc to persist them, so it must keep the in-memory state.
	if dataEdited {
		match, err = loadMatch(ctx, tx)
		if err != nil {
			return store.MatchView{}, nil, nil, err
		}
	}

	if err := store.RecalculateMatchResultsForStateTx(ctx, tx, match); err != nil {
		return store.MatchView{}, nil, nil, err
	}
	affected, err := resolver.ResolveGameSlotsTx(ctx, tx, match.GameID)
	if err != nil {
		return store.MatchView{}, nil, nil, err
	}
	revision, err := bumpMatchRevisionTx(ctx, tx, festID, match.MatchID, "match:update", util.MustJSON(reqs))
	if err != nil {
		return store.MatchView{}, nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return store.MatchView{}, nil, nil, err
	}

	view, err := loadView()
	if err != nil {
		return store.MatchView{}, nil, nil, err
	}
	if revision > 0 {
		view.Revision = util.MaxInt64(view.Revision, revision)
	}
	data, err := json.Marshal(view)
	if err != nil {
		return store.MatchView{}, nil, nil, err
	}

	// Load the views of downstream matches that changed, skipping the edited
	// match itself (already returned as `view`). Failures here are non-fatal —
	// the edit is committed; a missed cascade broadcast just costs a reload.
	var cascaded []store.MatchView
	for _, mid := range affected {
		if mid == match.MatchID {
			continue
		}
		cv, err := s.loadMatchViewByIDLocked(festID, match.GameID, mid)
		if err != nil || cv.Code == "" {
			continue
		}
		cascaded = append(cascaded, cv)
	}
	return view, data, cascaded, nil
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
	return matchViewFromDBState(match), nil
}

// applyMatchEditTx validates the request through the pure domain/matchedit rules,
// then applies the resulting plan as SQL (which needs the tx, so it stays here).
func applyMatchEditTx(ctx context.Context, tx *sql.Tx, match store.DBMatchState, req updateRequest) error {
	plan, err := matchedit.Validate(len(match.TeamIDs), func(i int) bool { return match.TeamIDs[i] != 0 }, len(store.QuestionValues), matchedit.Request{
		Action:      req.Action,
		HasTeamEdit: hasTeamEdit(req),
		Team:        req.Team,
		Tiebreak:    req.Tiebreak,
		Place:       req.Place,
		Theme:       req.Theme,
		Shootout:    req.Shootout,
		Answer:      req.Answer,
		Mark:        req.Mark,
		Player:      req.Player,
	})
	if err != nil {
		return err
	}

	switch plan.Action {
	case matchedit.ActionAddShootoutTheme:
		return festwrite.MutateMatchBlobTx(ctx, tx, match.MatchID, func(blob *store.MatchBlob) error {
			blob.AddShootoutTheme(match.TeamIDs)
			return nil
		})
	case matchedit.ActionRemoveShootoutTheme:
		return festwrite.MutateMatchBlobTx(ctx, tx, match.MatchID, func(blob *store.MatchBlob) error {
			return blob.RemoveShootoutTheme()
		})
	}

	teamID := match.TeamIDs[req.Team]

	if plan.Place != nil {
		// A host place edit is a pin: it lands in place_override (winning over
		// the scorer's place at every recompute) and mirrors into place so the
		// view is right before the recompute runs.
		if _, err := tx.ExecContext(ctx, `
insert into match_results(match_id, team_id, place, place_override)
values(?, ?, ?, ?)
on conflict(match_id, team_id) do update set place = excluded.place, place_override = excluded.place_override`,
			match.MatchID, teamID, *plan.Place, *plan.Place); err != nil {
			return err
		}
	}

	if plan.Theme != nil {
		var playerID int64
		if plan.Theme.Player != nil && *plan.Theme.Player != "" {
			id, err := lookupRosterPlayerID(ctx, tx, match.GameID, match.RosterSource, teamID, *plan.Theme.Player)
			if err != nil {
				return err
			}
			playerID = id
		}
		return festwrite.MutateMatchBlobTx(ctx, tx, match.MatchID, func(blob *store.MatchBlob) error {
			if plan.Theme.Player != nil {
				blob.SetPlayer(teamID, plan.Theme.Kind, plan.Theme.Index, playerID)
			}
			if plan.Theme.Answer != nil {
				blob.SetAnswer(teamID, plan.Theme.Kind, plan.Theme.Index, plan.Theme.Answer.Index, plan.Theme.Answer.Mark)
			}
			return nil
		})
	}

	return nil
}

func lookupRosterPlayerID(ctx context.Context, q store.Queryer, gameID int64, rosterSource string, teamID int64, player string) (int64, error) {
	rosterQuery := `
select p.id, p.first_name, p.last_name
from team_players tp
join players p on p.id = tp.player_id
where tp.team_id = ?`
	rosterArgs := []any{teamID}
	if rosterSource == "game" {
		rosterQuery = `
select p.id, p.first_name, p.last_name
from game_team_players gtp
join players p on p.id = gtp.player_id
where gtp.game_id = ? and gtp.team_id = ?`
		rosterArgs = []any{gameID, teamID}
	}
	rows, err := q.QueryContext(ctx, rosterQuery, rosterArgs...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var firstName, lastName string
		if err := rows.Scan(&id, &firstName, &lastName); err != nil {
			return 0, err
		}
		if store.JoinPlayerName(firstName, lastName) == player {
			return id, nil
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("player is not in roster")
}

func recalculateMatchResultsTx(ctx context.Context, tx *sql.Tx, festID int64, code string) error {
	match, err := store.LoadDBMatchState(ctx, tx, festID, code)
	if err != nil {
		return err
	}
	return store.RecalculateMatchResultsForStateTx(ctx, tx, match)
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

// updateScopedMatchVenue updates a match's venue and additionally returns
// deltaOps (set-ops vs the pre-edit view) when a delta broadcast beats the full
// view; nil otherwise.
func (s *server) updateScopedMatchVenue(ctx context.Context, scope matchScope, number int) (store.MatchView, []byte, []byte, error) {
	var oldData []byte
	view, data, err := s.updateMatchVenueUsing(ctx, scope.FestID, number,
		func(ctx context.Context, q store.Queryer) (store.DBMatchState, error) {
			return loadDBMatchStateByScope(ctx, q, scope)
		},
		func() (store.MatchView, error) {
			return s.loadScopedMatchViewLocked(scope)
		}, &oldData)
	if err != nil {
		return view, data, nil, err
	}
	deltaOps, _ := realtime.MatchDeltaOps(oldData, data)
	return view, data, deltaOps, nil
}

func (s *server) updateMatchVenueUsing(
	reqCtx context.Context,
	festID int64,
	number int,
	loadMatch func(context.Context, store.Queryer) (store.DBMatchState, error),
	loadView func() (store.MatchView, error),
	oldDataOut *[]byte,
) (store.MatchView, []byte, error) {
	if number <= 0 {
		return store.MatchView{}, nil, errors.New("bad venue number")
	}
	// Bounded, attributed write context + off-lock pool acquisition (see
	// applyMatchUpdateUsing / the 2026-06-13 freeze).
	ctx, cancel := festwrite.AuditDetachedContext(reqCtx, festID)
	defer cancel()
	conn, err := s.eng.AcquireWriteConn(ctx, "match-venue")
	if err != nil {
		return store.MatchView{}, nil, err
	}
	defer conn.Close()

	defer s.eng.LockWrite("match-venue")()

	// Pre-image under the exclusive lock for a minimal delta broadcast (see
	// applyMatchUpdateUsing). Best-effort; empty → caller sends full state.
	if oldDataOut != nil {
		if oldView, oerr := loadView(); oerr == nil {
			*oldDataOut, _ = json.Marshal(oldView)
		}
	}

	tx, err := s.eng.BeginWriteTxConn(ctx, conn)
	if err != nil {
		return store.MatchView{}, nil, err
	}
	defer tx.Rollback()

	match, err := loadMatch(ctx, tx)
	if err != nil {
		return store.MatchView{}, nil, err
	}
	var venueID int64
	if err := tx.QueryRowContext(ctx, `
select id from venues where fest_id = ? and number = ?`, festID, number).Scan(&venueID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.MatchView{}, nil, errors.New("unknown venue")
		}
		return store.MatchView{}, nil, err
	}
	if _, err := tx.ExecContext(ctx, `update matches set venue_id = ? where id = ?`, venueID, match.MatchID); err != nil {
		return store.MatchView{}, nil, err
	}
	revision, err := bumpMatchRevisionTx(ctx, tx, festID, match.MatchID, "match:venue", util.MustJSON(map[string]any{"code": match.Code, "venue": number}))
	if err != nil {
		return store.MatchView{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return store.MatchView{}, nil, err
	}
	view, err := loadView()
	if err != nil {
		return store.MatchView{}, nil, err
	}
	view.Revision = util.MaxInt64(view.Revision, revision)
	data, err := json.Marshal(view)
	return view, data, err
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
