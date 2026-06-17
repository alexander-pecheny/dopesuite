package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// gzipped JSON archive of a game: its full current state plus the edit history
// from audit_log. Host-only — it exposes raw before/after row snapshots and
// actor usernames that viewers shouldn't see. The goal is a self-contained
// "upload to S3 and forget" snapshot: everything needed to reconstruct the game
// offline, including the EK relational tree (stages → matches → slots/themes/
// answers/results) and the fest-level context (team/player names, venues).

// gameArchive is the on-disk shape of the .json.gz download. The export writes
// it as a gameArchiveHead followed by a streamed "auditLog" array (see
// handleScopedGameArchive); this type documents the full shape and is used by
// tests to parse a downloaded archive.
type gameArchive struct {
	Format     string                      `json:"format"`
	ExportedAt string                      `json:"exportedAt"`
	Game       gameArchiveGame             `json:"game"`
	Fest       map[string]any              `json:"fest,omitempty"`
	Rows       map[string][]map[string]any `json:"rows,omitempty"`    // game-scoped relational rows (EK)
	Context    map[string][]map[string]any `json:"context,omitempty"` // fest-level rows for name resolution
	AuditLog   []gameArchiveAudit          `json:"auditLog"`
}

// gameArchiveHead is everything in the archive except the (potentially huge)
// auditLog array, which is streamed separately so it never sits in memory whole.
type gameArchiveHead struct {
	Format     string                      `json:"format"`
	ExportedAt string                      `json:"exportedAt"`
	Game       gameArchiveGame             `json:"game"`
	Fest       map[string]any              `json:"fest,omitempty"`
	Rows       map[string][]map[string]any `json:"rows,omitempty"`
	Context    map[string][]map[string]any `json:"context,omitempty"`
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

// gameIDAuditTablesSQL is the quoted, comma-joined list of gameIDAuditTables for
// inlining into an IN (...) clause, so loadGameAuditTrail can decompress only
// those rows during its first pass.
var gameIDAuditTablesSQL = func() string {
	parts := make([]string, 0, len(gameIDAuditTables))
	for t := range gameIDAuditTables {
		parts = append(parts, "'"+t+"'")
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}()

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
	var gameSlug sql.NullString
	err := s.db.QueryRowContext(r.Context(), `
select code, title, game_type, status, revision, created_at, updated_at,
       coalesce(scheme_json, ''), coalesce(state_json, ''), slug
from games where fest_id = ? and id = ?`, scope.FestID, scope.GameID).
		Scan(&game.Code, &game.Title, &game.GameType, &game.Status, &game.Revision,
			&game.CreatedAt, &game.UpdatedAt, &schemeJSON, &stateJSON, &gameSlug)
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
	// The audit trail can be hundreds of MB once decompressed (the full
	// state_json is snapshotted before+after on nearly every edit), enough to OOM
	// a small VPS if buffered. Resolve only the row identities here, then stream
	// the rows one at a time into the gzip response below. Everything else in the
	// archive is bounded by fest size.
	auditIDs, err := s.gameAuditIncludedIDs(ctx, scope, anchors)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	head := gameArchiveHead{
		Format:     "dope.game-archive.v2",
		ExportedAt: utcNow(),
		Game:       game,
		Fest:       festRow,
		Rows:       rows,
		Context:    festContext,
	}
	headJSON, err := marshalNoHTMLEscape(head)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var festSlug string
	if s, ok := festRow["slug"].(string); ok {
		festSlug = s
	}
	w.Header().Set("Content-Type", "application/gzip")
	stem := exportFileStem(festSlug, scope.FestID, gameSlug.String, scope.GameID)
	w.Header().Set("Content-Disposition", contentDispositionAttachment(stem+".json.gz"))
	gz := gzip.NewWriter(w)
	defer gz.Close()

	// Splice the streamed "auditLog" array into the head object: write the head
	// object minus its closing brace, then the array element-by-element, then close.
	if _, err := gz.Write(headJSON[:len(headJSON)-1]); err != nil {
		return
	}
	if _, err := io.WriteString(gz, `,"auditLog":[`); err != nil {
		return
	}
	enc := json.NewEncoder(gz)
	enc.SetEscapeHTML(false)
	first := true
	streamErr := s.streamGameAuditTrail(ctx, auditIDs, func(e *gameArchiveAudit) error {
		if !first {
			if _, err := io.WriteString(gz, ","); err != nil {
				return err
			}
		}
		first = false
		return enc.Encode(e) // trailing newline is harmless whitespace inside the array
	})
	if streamErr != nil {
		// Partial body already flushed; nothing useful to send.
		return
	}
	_, _ = io.WriteString(gz, `]}`)
}

// marshalNoHTMLEscape JSON-encodes v without escaping <, >, & (matching the
// archive's encoder settings) and strips the encoder's trailing newline.
func marshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
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

// gameAuditIncludedIDs returns, oldest-first, the audit_log ids that make up
// this game's edit history. A row belongs to the game when it is directly
// attributable (matches a current row identity, carries the game_id, or is the
// games row) or is a child-table row sharing a request with a directly
// attributable row (cascade deletes).
//
// This is the cheap half of the export: it scans the whole fest log but only
// decompresses the small game_id-bearing config tables (needed for the game_id
// fallback) — everything else is attributed by table_name/row_pk/request_id. The
// heavy before/after snapshots (hundreds of MB once unzipped, dominated by the
// per-edit games.state_json) are read later, one row at a time, by
// streamGameAuditTrail.
func (s *server) gameAuditIncludedIDs(ctx context.Context, scope festScope, anchors map[string]bool) ([]int64, error) {
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

	gameRequests := map[string]bool{}
	included := map[int64]bool{}
	type childRef struct {
		id  int64
		req string
	}
	var childRows []childRef
	scan1, err := s.db.QueryContext(ctx, `
select id, table_name, row_pk, coalesce(request_id, ''),
       case when table_name in (`+gameIDAuditTablesSQL+`) then dope_unz(before_json) end,
       case when table_name in (`+gameIDAuditTablesSQL+`) then dope_unz(after_json) end
from audit_log where fest_id = ? order by id`, scope.FestID)
	if err != nil {
		return nil, err
	}
	func() {
		defer scan1.Close()
		for scan1.Next() {
			var id int64
			var table, rowPK, req string
			var before, after sql.NullString
			if err = scan1.Scan(&id, &table, &rowPK, &req, &before, &after); err != nil {
				return
			}
			if directlyGame(table, rowPK, before, after) {
				included[id] = true
				if req != "" {
					gameRequests[req] = true
				}
			} else if req != "" && childAuditTables[table] {
				childRows = append(childRows, childRef{id, req})
			}
		}
		err = scan1.Err()
	}()
	if err != nil {
		return nil, err
	}

	// Child-table rows (no game_id of their own) join the game when they share a
	// request with a directly-attributable row — this captures cascade deletes.
	for _, c := range childRows {
		if gameRequests[c.req] {
			included[c.id] = true
		}
	}
	ids := make([]int64, 0, len(included))
	for id := range included {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// streamGameAuditTrail decompresses before/after for the given audit ids
// (oldest-first) and invokes emit once per row, so the caller can write each
// row straight to the response without ever holding the full trail in memory.
// Rows are fetched in id-ordered batches to stay under SQLite's parameter limit.
func (s *server) streamGameAuditTrail(ctx context.Context, ids []int64, emit func(*gameArchiveAudit) error) error {
	const batch = 900 // stay well under SQLite's bound-parameter limit
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		ph := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			ph[i] = "?"
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, `
select a.id, a.ts, a.op, a.table_name, a.row_pk, coalesce(a.request_id, ''),
       coalesce(u.username, ''), dope_unz(a.before_json), dope_unz(a.after_json)
from audit_log a
left join users u on u.id = a.actor_user_id
where a.id in (`+strings.Join(ph, ",")+`)
order by a.id`, args...)
		if err != nil {
			return err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var (
					e      gameArchiveAudit
					before sql.NullString
					after  sql.NullString
				)
				if err := rows.Scan(&e.ID, &e.Ts, &e.Op, &e.TableName, &e.RowPK,
					&e.RequestID, &e.Actor, &before, &after); err != nil {
					return err
				}
				if before.Valid {
					e.Before = rawJSONOrNull(before.String)
				}
				if after.Valid {
					e.After = rawJSONOrNull(after.String)
				}
				if err := emit(&e); err != nil {
					return err
				}
			}
			return rows.Err()
		}()
		if err != nil {
			return err
		}
	}
	return nil
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
