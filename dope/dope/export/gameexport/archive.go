package gameexport

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dope/dope/storage/store"
)

// gzipped JSON archive of a game: its full current state plus the edit history
// from audit_log. Host-only — it exposes raw before/after row snapshots and
// actor usernames that viewers shouldn't see. The goal is a self-contained
// "upload to S3 and forget" snapshot: everything needed to reconstruct the game
// offline, including the EK relational tree (stages → matches → slots/themes/
// answers/results) and the fest-level context (team/player names, venues).

// GameArchive is the on-disk shape of the .json.gz download. The export writes
// it as a GameArchiveHead (see HandleScopedGameArchive); this type documents the
// full shape and is used by tests to parse a downloaded archive.
type GameArchive struct {
	Format     string                      `json:"format"`
	ExportedAt string                      `json:"exportedAt"`
	Game       GameArchiveGame             `json:"game"`
	Fest       map[string]any              `json:"fest,omitempty"`
	Rows       map[string][]map[string]any `json:"rows,omitempty"`    // game-scoped relational rows (EK)
	Context    map[string][]map[string]any `json:"context,omitempty"` // fest-level rows for name resolution
}

// GameArchiveHead is everything in the archive except the (potentially huge)
// auditLog array, which is streamed separately so it never sits in memory whole.
type GameArchiveHead struct {
	Format     string                      `json:"format"`
	ExportedAt string                      `json:"exportedAt"`
	Game       GameArchiveGame             `json:"game"`
	Fest       map[string]any              `json:"fest,omitempty"`
	Rows       map[string][]map[string]any `json:"rows,omitempty"`
	Context    map[string][]map[string]any `json:"context,omitempty"`
}

type GameArchiveGame struct {
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

// HandleScopedGameArchive serves GET /api/fest/{fid}/games/{gid}/export.json.gz.
// Host-only (table-editor role).
func HandleScopedGameArchive(s Host, w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.RequireFestTableEditor(w, r, festID) {
		return
	}

	game := GameArchiveGame{FestID: festID, GameID: gameID}
	var schemeJSON, stateJSON string
	var gameSlug sql.NullString
	err := s.DB().QueryRowContext(r.Context(), `
select code, title, game_type, status, revision, created_at, updated_at,
       coalesce(scheme_json, ''), coalesce(state_json, ''), slug
from games where fest_id = ? and id = ?`, festID, gameID).
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
	rows, _, err := loadGameRelationalRows(ctx, s.DB(), gameID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	festRow, festContext, err := loadGameArchiveContext(ctx, s.DB(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	head := GameArchiveHead{
		Format:     "dope.game-archive.v2",
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
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
	stem := ExportFileStem(festSlug, festID, gameSlug.String, gameID)
	w.Header().Set("Content-Disposition", ContentDispositionAttachment(stem+".json.gz"))
	gz := gzip.NewWriter(w)
	defer gz.Close()

	// The archive captures the game's full current state. Edit history is no
	// longer embedded here (the old before/after audit trail is retired); it now
	// lives in the per-game journal (see pages_host_journal.go).
	if _, err := gz.Write(headJSON); err != nil {
		return
	}
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
func loadGameRelationalRows(ctx context.Context, db *sql.DB, gameID int64) (map[string][]map[string]any, map[string]bool, error) {
	rows := make(map[string][]map[string]any)
	anchors := map[string]bool{auditAnchorKey("games", strconv.FormatInt(gameID, 10)): true}
	for _, spec := range gameRelSpecs {
		got, err := queryRowMaps(ctx, db, spec.query, gameID)
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
func loadGameArchiveContext(ctx context.Context, db *sql.DB, festID int64) (map[string]any, map[string][]map[string]any, error) {
	var festRow map[string]any
	fests, err := queryRowMaps(ctx, db, `select * from fests where id = ?`, festID)
	if err != nil {
		return nil, nil, fmt.Errorf("archive load fest: %w", err)
	}
	if len(fests) > 0 {
		festRow = fests[0]
	}
	context := make(map[string][]map[string]any)
	for _, spec := range festContextSpecs {
		got, err := queryRowMaps(ctx, db, spec.query, festID)
		if err != nil {
			return nil, nil, fmt.Errorf("archive load %s: %w", spec.table, err)
		}
		if len(got) > 0 {
			context[spec.table] = got
		}
	}
	return festRow, context, nil
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

// queryRowMaps runs a query and returns each row as a column→value map. Text
// columns whose name ends in "_json" are embedded as raw JSON when valid, so the
// archive stays browsable rather than double-escaped.
func queryRowMaps(ctx context.Context, q store.Queryer, query string, args ...any) ([]map[string]any, error) {
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
