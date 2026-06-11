package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// gzipped JSON archive of a game: its full current state plus the edit history
// from audit_log. Host-only — it exposes raw before/after row snapshots and
// actor usernames that viewers shouldn't see. The goal is a self-contained
// "upload to S3 and forget" snapshot: everything needed to reconstruct the game
// offline, including the EK relational tree (stages → matches → slots/themes/
// answers/results) and the fest-level context (team/player names, venues).

// gameArchive is the on-disk shape of the .json.gz download.
type gameArchive struct {
	Format     string                      `json:"format"`
	ExportedAt string                      `json:"exportedAt"`
	Game       gameArchiveGame             `json:"game"`
	Fest       map[string]any              `json:"fest,omitempty"`
	Rows       map[string][]map[string]any `json:"rows,omitempty"`     // game-scoped relational rows (EK)
	Context    map[string][]map[string]any `json:"context,omitempty"`  // fest-level rows for name resolution
	AuditLog   []gameArchiveAudit          `json:"auditLog"`
}

type gameArchiveGame struct {
	FestID    int64           `json:"festID"`
	GameID    int64           `json:"gameID"`
	Code      string          `json:"code"`
	Title     string          `json:"title"`
	GameType  string          `json:"gameType"`
	Status    string          `json:"status"`
	Revision  int64           `json:"revision"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
	Scheme    json.RawMessage `json:"scheme"`
	State     json.RawMessage `json:"state"`
}

type gameArchiveAudit struct {
	ID        int64           `json:"id"`
	Ts        string          `json:"ts"`
	Op        string          `json:"op"`
	TableName string          `json:"tableName"`
	RowPK     string          `json:"rowPK"`
	RequestID string          `json:"requestID,omitempty"`
	Actor     string          `json:"actor,omitempty"`
	Before    json.RawMessage `json:"before,omitempty"`
	After     json.RawMessage `json:"after,omitempty"`
}

// gameRelSpec describes a game-scoped audited table: how to load its current
// rows for this game and the primary-key columns used to reconstruct the
// audit_log row_pk (see buildAuditTrigger's pkExpr) for identity matching.
type gameRelSpec struct {
	table string
	query string // single ? placeholder bound to gameID
	pk    []string
}

// gameRelSpecs covers every audited table whose rows belong to a single game,
// reached either by a direct game_id column or by tracing match_id/theme_id/
// stage_id back to the game. Order is parent-first for readability.
var gameRelSpecs = []gameRelSpec{
	{"stages", `select * from stages where game_id = ?`, []string{"id"}},
	{"matches", `select * from matches where game_id = ?`, []string{"id"}},
	{"match_slots", `select * from match_slots where match_id in (select id from matches where game_id = ?)`, []string{"id"}},
	{"themes", `select * from themes where match_id in (select id from matches where game_id = ?)`, []string{"id"}},
	{"answers", `select * from answers where theme_id in (select th.id from themes th join matches m on m.id = th.match_id where m.game_id = ?)`, []string{"id"}},
	{"match_results", `select * from match_results where match_id in (select id from matches where game_id = ?)`, []string{"match_id", "team_id"}},
	{"reseed_entries", `select * from reseed_entries where stage_id in (select id from stages where game_id = ?)`, []string{"stage_id", "rank"}},
	{"game_teams", `select * from game_teams where game_id = ?`, []string{"game_id", "team_id"}},
	{"game_players", `select * from game_players where game_id = ?`, []string{"game_id", "player_id"}},
	{"game_team_players", `select * from game_team_players where game_id = ?`, []string{"game_id", "team_id", "player_id"}},
	{"game_assignments", `select * from game_assignments where game_id = ?`, []string{"game_id", "basket", "number"}},
	{"game_player_team_overrides", `select * from game_player_team_overrides where game_id = ?`, []string{"fest_id", "game_id", "player_id"}},
}

// gameIDAuditTables are game-scoped audited tables whose row carries a game_id
// (or, for games, whose pk is the game id), so an audit snapshot can be
// attributed to a game directly. Used to catch the history of since-deleted
// stages/matches that no longer appear in the current relational state.
var gameIDAuditTables = map[string]bool{
	"stages": true, "matches": true,
	"game_teams": true, "game_players": true, "game_team_players": true,
	"game_assignments": true, "game_player_team_overrides": true,
}

// childAuditTables hang off a match/theme/stage and carry no game_id of their
// own. They are attributed to a game by sharing a request_id with a row that
// IS directly attributable — which naturally captures cascade deletes (deleting
// a match emits its slots/themes/answers/results under the same request).
var childAuditTables = map[string]bool{
	"match_slots": true, "themes": true, "answers": true,
	"match_results": true, "reseed_entries": true,
}

// festContextSpecs are fest-level tables included so names/venues referenced by
// the game's relational rows resolve offline. Bounded by fest size.
var festContextSpecs = []struct {
	table string
	query string // single ? placeholder bound to festID
}{
	{"fest_teams", `select * from fest_teams where fest_id = ?`},
	{"fest_players", `select * from fest_players where fest_id = ?`},
	{"fest_team_players", `select * from fest_team_players where team_id in (select id from fest_teams where fest_id = ?)`},
	{"teams", `select * from teams where fest_id = ?`},
	{"players", `select * from players where fest_id = ?`},
	{"team_players", `select * from team_players where team_id in (select id from teams where fest_id = ?)`},
	{"venues", `select * from venues where fest_id = ?`},
}

// handleScopedGameArchive serves GET /api/fest/{fid}/games/{gid}/export.json.gz.
// Host-only (table-editor role).
func (s *server) handleScopedGameArchive(w http.ResponseWriter, r *http.Request, scope festScope) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.requireFestTableEditor(w, r, scope.FestID); !ok {
		return
	}

	game := gameArchiveGame{FestID: scope.FestID, GameID: scope.GameID}
	var schemeJSON, stateJSON string
	err := s.db.QueryRowContext(r.Context(), `
select code, title, game_type, status, revision, created_at, updated_at,
       coalesce(scheme_json, ''), coalesce(state_json, '')
from games where fest_id = ? and id = ?`, scope.FestID, scope.GameID).
		Scan(&game.Code, &game.Title, &game.GameType, &game.Status, &game.Revision,
			&game.CreatedAt, &game.UpdatedAt, &schemeJSON, &stateJSON)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	game.Scheme = rawJSONOrNull(schemeJSON)
	game.State = rawJSONOrNull(stateJSON)

	ctx := r.Context()
	rows, anchors, err := s.loadGameRelationalRows(ctx, scope.GameID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	festRow, festContext, err := s.loadGameArchiveContext(ctx, scope.FestID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	auditLog, err := s.loadGameAuditTrail(ctx, scope, anchors)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	archive := gameArchive{
		Format:     "dope.game-archive.v2",
		ExportedAt: utcNow(),
		Game:       game,
		Fest:       festRow,
		Rows:       rows,
		Context:    festContext,
		AuditLog:   auditLog,
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", contentDispositionAttachment(archiveFileName(game.Title, game.GameType)))
	gz := gzip.NewWriter(w)
	defer gz.Close()
	enc := json.NewEncoder(gz)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(archive); err != nil {
		// Headers/body may already be partially flushed; nothing useful to send.
		return
	}
}

// loadGameRelationalRows loads the current rows of every game-scoped table for
// gameID and returns them keyed by table, plus the set of audit-log row
// identities ("table\x00row_pk") those rows occupy, used to attribute audit
// history. Tables with no rows are omitted from the map.
func (s *server) loadGameRelationalRows(ctx context.Context, gameID int64) (map[string][]map[string]any, map[string]bool, error) {
	rows := make(map[string][]map[string]any)
	anchors := map[string]bool{auditAnchorKey("games", strconv.FormatInt(gameID, 10)): true}
	for _, spec := range gameRelSpecs {
		got, err := queryRowMaps(ctx, s.db, spec.query, gameID)
		if err != nil {
			return nil, nil, fmt.Errorf("archive load %s: %w", spec.table, err)
		}
		if len(got) == 0 {
			continue
		}
		rows[spec.table] = got
		for _, m := range got {
			anchors[auditAnchorKey(spec.table, rowPKFromMap(m, spec.pk))] = true
		}
	}
	return rows, anchors, nil
}

// loadGameArchiveContext loads the fest row and the fest-level lookup tables.
func (s *server) loadGameArchiveContext(ctx context.Context, festID int64) (map[string]any, map[string][]map[string]any, error) {
	var festRow map[string]any
	fests, err := queryRowMaps(ctx, s.db, `select * from fests where id = ?`, festID)
	if err != nil {
		return nil, nil, fmt.Errorf("archive load fest: %w", err)
	}
	if len(fests) > 0 {
		festRow = fests[0]
	}
	context := make(map[string][]map[string]any)
	for _, spec := range festContextSpecs {
		got, err := queryRowMaps(ctx, s.db, spec.query, festID)
		if err != nil {
			return nil, nil, fmt.Errorf("archive load %s: %w", spec.table, err)
		}
		if len(got) > 0 {
			context[spec.table] = got
		}
	}
	return festRow, context, nil
}

// loadGameAuditTrail collects this game's edit history from the fest-scoped
// audit_log, oldest first. A row is included when it is directly attributable to
// the game (matches a current row identity, or carries the game_id, or is the
// games row) or when it is a child-table row sharing a request with a directly
// attributable row (cascade deletes). Snapshots are decompressed with dope_unz.
func (s *server) loadGameAuditTrail(ctx context.Context, scope festScope, anchors map[string]bool) ([]gameArchiveAudit, error) {
	gidStr := strconv.FormatInt(scope.GameID, 10)
	directlyGame := func(table, rowPK string, before, after sql.NullString) bool {
		if anchors[auditAnchorKey(table, rowPK)] {
			return true
		}
		if table == "games" {
			return rowPK == gidStr
		}
		if gameIDAuditTables[table] {
			if id, ok := snapshotInt(before, after, "game_id"); ok {
				return id == scope.GameID
			}
		}
		return false
	}

	// Pass 1: collect the request_ids that touch this game.
	gameRequests := map[string]bool{}
	scan1, err := s.db.QueryContext(ctx, `
select table_name, row_pk, coalesce(request_id, ''), dope_unz(before_json), dope_unz(after_json)
from audit_log where fest_id = ? order by id`, scope.FestID)
	if err != nil {
		return nil, err
	}
	func() {
		defer scan1.Close()
		for scan1.Next() {
			var table, rowPK, req string
			var before, after sql.NullString
			if err = scan1.Scan(&table, &rowPK, &req, &before, &after); err != nil {
				return
			}
			if req != "" && directlyGame(table, rowPK, before, after) {
				gameRequests[req] = true
			}
		}
		err = scan1.Err()
	}()
	if err != nil {
		return nil, err
	}

	// Pass 2: emit every row attributable to the game.
	scan2, err := s.db.QueryContext(ctx, `
select a.id, a.ts, a.op, a.table_name, a.row_pk, coalesce(a.request_id, ''),
       coalesce(u.username, ''), dope_unz(a.before_json), dope_unz(a.after_json)
from audit_log a
left join users u on u.id = a.actor_user_id
where a.fest_id = ?
order by a.id`, scope.FestID)
	if err != nil {
		return nil, err
	}
	defer scan2.Close()
	var out []gameArchiveAudit
	for scan2.Next() {
		var (
			e      gameArchiveAudit
			before sql.NullString
			after  sql.NullString
		)
		if err := scan2.Scan(&e.ID, &e.Ts, &e.Op, &e.TableName, &e.RowPK,
			&e.RequestID, &e.Actor, &before, &after); err != nil {
			return nil, err
		}
		include := directlyGame(e.TableName, e.RowPK, before, after) ||
			(childAuditTables[e.TableName] && e.RequestID != "" && gameRequests[e.RequestID])
		if !include {
			continue
		}
		if before.Valid {
			e.Before = rawJSONOrNull(before.String)
		}
		if after.Valid {
			e.After = rawJSONOrNull(after.String)
		}
		out = append(out, e)
	}
	return out, scan2.Err()
}

// archiveFileName derives the .json.gz download name from the game title,
// falling back to the game type, mirroring exportFileName.
func archiveFileName(title, gameType string) string {
	base := sanitizeFileName(title)
	if base == "" {
		base = gameType
	}
	return base + ".json.gz"
}

// rawJSONOrNull returns s as raw JSON when it is non-empty, else JSON null, so
// the archive embeds parsed objects instead of escaped strings.
func rawJSONOrNull(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage("null")
	}
	return json.RawMessage(s)
}

func auditAnchorKey(table, rowPK string) string { return table + "\x00" + rowPK }

// rowPKFromMap reconstructs the audit_log row_pk for a row map: the pk columns
// cast to text and joined with ':' (matching buildAuditTrigger's pkExpr).
func rowPKFromMap(m map[string]any, pk []string) string {
	parts := make([]string, len(pk))
	for i, c := range pk {
		parts[i] = scalarText(m[c])
	}
	return strings.Join(parts, ":")
}

// scalarText renders a scalar column value the way SQLite's CAST(x AS TEXT)
// would for the integer primary keys used in row_pk.
func scalarText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// snapshotInt reads an integer field from an audit before/after snapshot,
// preferring after (INSERT/UPDATE) then before (DELETE).
func snapshotInt(before, after sql.NullString, field string) (int64, bool) {
	for _, s := range []sql.NullString{after, before} {
		if !s.Valid || s.String == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(s.String), &m) != nil {
			continue
		}
		if v, ok := m[field]; ok {
			switch n := v.(type) {
			case float64:
				return int64(n), true
			case json.Number:
				if i, err := n.Int64(); err == nil {
					return i, true
				}
			}
		}
	}
	return 0, false
}

// queryRowMaps runs a query and returns each row as a column→value map. Text
// columns whose name ends in "_json" are embedded as raw JSON when valid, so the
// archive stays browsable rather than double-escaped.
func queryRowMaps(ctx context.Context, q dbQueryer, query string, args ...any) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c] = normalizeColValue(c, vals[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func normalizeColValue(col string, v any) any {
	asBytes := func(b []byte) any {
		if strings.HasSuffix(col, "_json") && json.Valid(b) {
			return json.RawMessage(append([]byte(nil), b...))
		}
		return string(b)
	}
	switch t := v.(type) {
	case []byte:
		return asBytes(t)
	case string:
		if strings.HasSuffix(col, "_json") && json.Valid([]byte(t)) {
			return json.RawMessage(t)
		}
		return t
	default:
		return v
	}
}
