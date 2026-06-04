package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type festScope struct {
	FestID int64
	GameID int64
}

func parseScopedPath(path, prefix string) (festScope, string, bool) {
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return festScope{}, "", false
	}
	rest = strings.TrimPrefix(rest, "/")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) < 2 {
		return festScope{}, "", false
	}
	tid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || tid <= 0 {
		return festScope{}, "", false
	}
	if parts[1] != "games" {
		return festScope{}, "", false
	}
	if len(parts) < 3 {
		return festScope{FestID: tid}, "", true
	}
	gid, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || gid <= 0 {
		return festScope{}, "", false
	}
	scope := festScope{FestID: tid, GameID: gid}
	if len(parts) < 4 {
		return scope, "", true
	}
	return scope, parts[3], true
}

func (s *server) verifyMatchInScope(ctx context.Context, scope festScope, code string) (matchScope, error) {
	row := s.db.QueryRowContext(ctx, `
select id from matches where fest_id = ? and game_id = ? and code = ?`,
		scope.FestID, scope.GameID, code)
	var matchID int64
	if err := row.Scan(&matchID); err != nil {
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
	return fmt.Sprintf("match:%d:%s", scope.GameID, scope.Code)
}

// broadcastMatchCascade fans out the views of downstream matches whose slots
// changed when an edit resolved the bracket, so spectators on those matches (or
// the grid) see advancing teams live instead of only on reload.
func (s *server) broadcastMatchCascade(festID, gameID int64, cascaded []MatchView) {
	for _, cv := range cascaded {
		data, err := json.Marshal(cv)
		if err != nil {
			continue
		}
		scopeKey := matchScopeKey(matchScope{festScope: festScope{FestID: festID, GameID: gameID}, Code: cv.Code})
		s.broadcastState(festID, scopeKey, cv.Revision, data)
	}
}

var errMatchNotFound = errors.New("match not found in this game")
var errRatingRosterImmutable = errors.New("команды загружаются из rating.chgk.info; чтобы изменить список, переимпортируйте участников")

type gameStatePatchRequest struct {
	Ops []gameStatePatchOp `json:"ops"`
}

type gameStatePatchOp struct {
	Op    string            `json:"op,omitempty"`
	Path  []json.RawMessage `json:"path"`
	Value json.RawMessage   `json:"value"`
}

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

type jsonPathSegment struct {
	key     string
	index   int
	isIndex bool
}

func (s *server) festVisibility(ctx context.Context, festID int64) (bool, bool, error) {
	if s.db == nil {
		return false, false, nil
	}
	var isPublic int
	err := s.db.QueryRowContext(ctx, `select is_public from fests where id = ?`, festID).Scan(&isPublic)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, isPublic == 1, nil
}

func (s *server) authorizeFestRead(w http.ResponseWriter, r *http.Request, festID int64) bool {
	exists, public, err := s.festVisibility(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	if !exists {
		http.NotFound(w, r)
		return false
	}
	if public {
		return true
	}
	user, ok := s.lookupSession(r)
	if !ok {
		http.NotFound(w, r)
		return false
	}
	role, err := s.festUserRole(r.Context(), festID, user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	if role == "" {
		http.NotFound(w, r)
		return false
	}
	return true
}

func (s *server) requireFestOrganizer(w http.ResponseWriter, r *http.Request, festID int64) (sessionUser, bool) {
	user, _, ok := s.requireFestRole(w, r, festID, festRoleCanEditGameTables)
	return user, ok
}

func (s *server) requireFestAdmin(w http.ResponseWriter, r *http.Request, festID int64) (sessionUser, bool) {
	user, _, ok := s.requireFestRole(w, r, festID, festRoleCanManageFest)
	return user, ok
}

func (s *server) requireFestTableEditor(w http.ResponseWriter, r *http.Request, festID int64) (sessionUser, bool) {
	user, _, ok := s.requireFestRole(w, r, festID, festRoleCanEditGameTables)
	return user, ok
}

func (s *server) requireFestRole(w http.ResponseWriter, r *http.Request, festID int64, allowed func(string) bool) (sessionUser, string, bool) {
	if !requireSameOriginUnsafe(w, r) {
		return sessionUser{}, "", false
	}
	user, ok := s.lookupSession(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return sessionUser{}, "", false
	}
	role, err := s.festUserRole(r.Context(), festID, user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return sessionUser{}, "", false
	}
	if allowed(role) {
		return user, role, true
	}
	exists, _, err := s.festVisibility(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return sessionUser{}, "", false
	}
	if !exists {
		http.NotFound(w, r)
		return sessionUser{}, "", false
	}
	http.Error(w, "forbidden", http.StatusForbidden)
	return sessionUser{}, "", false
}

func (s *server) authorizeHostPresence(w http.ResponseWriter, r *http.Request, festID int64) bool {
	user, ok := s.lookupSession(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	role, err := s.festUserRole(r.Context(), festID, user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	if festRoleCanEditGameTables(role) {
		return true
	}
	exists, _, err := s.festVisibility(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	if !exists {
		http.NotFound(w, r)
		return false
	}
	http.Error(w, "forbidden", http.StatusForbidden)
	return false
}

// /api/fest/{tid}
// /api/fest/{tid}/venues
// /api/fest/{tid}/venues/{n}
// /api/fest/{tid}/games/{gid}
// /api/fest/{tid}/games/{gid}/matches/{code}
// /api/fest/{tid}/games/{gid}/matches/{code}/update
// /api/fest/{tid}/games/{gid}/matches/{code}/finish
// /api/fest/{tid}/games/{gid}/matches/{code}/venue
// /api/fest/{tid}/games/{gid}/seed-import
func (s *server) handleScopedAPI(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/fest/")
	if rest == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	tid, err := resolveFestID(r.Context(), s.db, parts[0])
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tid <= 0 {
		http.NotFound(w, r)
		return
	}

	if len(parts) == 1 {
		s.handleScopedFest(w, r, tid)
		return
	}

	switch parts[1] {
	case "presence":
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		s.handleHostPresence(w, r, tid)
		return
	case "venues":
		s.handleScopedVenues(w, r, tid, parts[2:])
		return
	case "games":
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
		gid, err := resolveGameID(r.Context(), s.db, tid, parts[2])
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if gid <= 0 {
			http.NotFound(w, r)
			return
		}
		scope := festScope{FestID: tid, GameID: gid}
		if len(parts) == 3 {
			s.handleScopedGame(w, r, scope)
			return
		}
		switch parts[3] {
		case "matches":
			s.handleScopedMatches(w, r, scope, parts[4:])
			return
		case "stages":
			s.handleScopedStages(w, r, scope, parts[4:])
			return
		case "state":
			if len(parts) != 4 {
				http.NotFound(w, r)
				return
			}
			s.handleScopedGameState(w, r, scope)
			return
		case "scheme":
			if len(parts) != 4 {
				http.NotFound(w, r)
				return
			}
			s.handleScopedGameScheme(w, r, scope)
			return
		case "seed-import":
			s.handleScopedSeedImport(w, r, scope, parts[4:])
			return
		}
	}
	http.NotFound(w, r)
}

func (s *server) handleHostPresence(w http.ResponseWriter, r *http.Request, festID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.requireFestTableEditor(w, r, festID)
	if !ok {
		return
	}
	defer r.Body.Close()
	var req hostPresenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	if active {
		if len(req.Cursor) == 0 || !json.Valid(req.Cursor) {
			http.Error(w, "bad cursor", http.StatusBadRequest)
			return
		}
	} else {
		req.Cursor = nil
	}

	username := fmt.Sprintf("user-%d", user.UserID)
	if user.Username.Valid && strings.TrimSpace(user.Username.String) != "" {
		username = user.Username.String
	}
	msg := hostPresenceMessage{
		UserID:    user.UserID,
		Username:  username,
		Color:     hostPresenceColor(user.UserID),
		Active:    active,
		Cursor:    req.Cursor,
		UpdatedAt: utcNow(),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.broadcastHostPresence(hostPresenceEvent{festID: festID, data: data})
	writeJSON(w, data)
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

func (s *server) handleScopedGameState(w http.ResponseWriter, r *http.Request, scope festScope) {
	switch r.Method {
	case http.MethodGet:
		if !s.authorizeFestRead(w, r, scope.FestID) {
			return
		}
		var stateJSON string
		err := s.db.QueryRowContext(r.Context(), `
	select state_json from games where fest_id = ? and id = ?`, scope.FestID, scope.GameID).Scan(&stateJSON)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if stateJSON == "" {
			stateJSON = "{}"
		}
		// X-State-Seq lets a resyncing SSE client align its lastSeq with the
		// state it just fetched, so the next delta chains cleanly.
		w.Header().Set("X-State-Seq", strconv.FormatUint(s.currentStateSeq(fmt.Sprintf("game-state:%d", scope.GameID)), 10))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(stateJSON))
	case http.MethodPut:
		if _, ok := s.requireFestTableEditor(w, r, scope.FestID); !ok {
			return
		}
		defer r.Body.Close()
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !json.Valid(raw) {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		revision, err := s.replaceGameState(r.Context(), scope, raw)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, errRatingRosterImmutable) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcastState(scope.FestID, fmt.Sprintf("game-state:%d", scope.GameID), revision, raw)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(raw)
	case http.MethodPatch:
		if _, ok := s.requireFestTableEditor(w, r, scope.FestID); !ok {
			return
		}
		defer r.Body.Close()
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req gameStatePatchRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		next, revision, err := s.patchGameState(r.Context(), scope, req, string(raw))
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Broadcast the ops we just applied as a scoped delta — viewers apply
		// them in place instead of receiving the whole state blob. If marshalling
		// the ops fails, fall back to a full-state snapshot so viewers still sync.
		scopeKey := fmt.Sprintf("game-state:%d", scope.GameID)
		if opsJSON, mErr := json.Marshal(req.Ops); mErr == nil {
			s.broadcastStateDelta(scope.FestID, scopeKey, revision, opsJSON)
		} else {
			s.broadcastState(scope.FestID, scopeKey, revision, next)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(next)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) replaceGameState(ctx context.Context, scope festScope, raw []byte) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var gameType, stateJSON string
	if err := tx.QueryRowContext(ctx, `
select game_type, state_json from games where fest_id = ? and id = ?`,
		scope.FestID, scope.GameID).Scan(&gameType, &stateJSON); err != nil {
		return 0, err
	}
	if err := validateImmutableRatingRosterState(gameType, []byte(stateJSON), raw); err != nil {
		return 0, err
	}

	result, err := tx.ExecContext(ctx, `
update games set state_json = ?, updated_at = ? where fest_id = ? and id = ?`,
		string(raw), utcNow(), scope.FestID, scope.GameID)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, sql.ErrNoRows
	}
	revision, err := bumpFestRevisionTx(ctx, tx, scope.FestID, "game:state", string(raw))
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return revision, nil
}

func (s *server) patchGameState(ctx context.Context, scope festScope, req gameStatePatchRequest, payload string) ([]byte, int64, error) {
	if len(req.Ops) == 0 {
		return nil, 0, errors.New("missing patch ops")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()

	var gameType, stateJSON string
	if err := tx.QueryRowContext(ctx, `
select game_type, state_json from games where fest_id = ? and id = ?`,
		scope.FestID, scope.GameID).Scan(&gameType, &stateJSON); err != nil {
		return nil, 0, err
	}
	if stateJSON == "" {
		stateJSON = "{}"
	}

	var root any
	if err := json.Unmarshal([]byte(stateJSON), &root); err != nil {
		return nil, 0, fmt.Errorf("stored game state is invalid json: %w", err)
	}
	if root == nil {
		root = map[string]any{}
	}

	for _, op := range req.Ops {
		if op.Op != "" && op.Op != "set" {
			return nil, 0, fmt.Errorf("unsupported patch op %q", op.Op)
		}
		path, err := parseJSONPatchPath(op.Path)
		if err != nil {
			return nil, 0, err
		}
		if patchPathTouchesRatingRoster(gameType, path) {
			return nil, 0, errRatingRosterImmutable
		}
		value, err := decodePatchValue(op.Value)
		if err != nil {
			return nil, 0, err
		}
		root, err = applyJSONSet(root, path, value)
		if err != nil {
			return nil, 0, err
		}
	}

	next, err := json.Marshal(root)
	if err != nil {
		return nil, 0, err
	}
	result, err := tx.ExecContext(ctx, `
update games set state_json = ?, updated_at = ? where fest_id = ? and id = ?`,
		string(next), utcNow(), scope.FestID, scope.GameID)
	if err != nil {
		return nil, 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, 0, err
	}
	if n == 0 {
		return nil, 0, sql.ErrNoRows
	}
	revision, err := bumpFestRevisionTx(ctx, tx, scope.FestID, "game:state-patch", payload)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return next, revision, nil
}

func patchPathTouchesRatingRoster(gameType string, path []jsonPathSegment) bool {
	key, ok := immutableRatingRosterStateKey(gameType)
	return ok && len(path) > 0 && !path[0].isIndex && path[0].key == key
}

func validateImmutableRatingRosterState(gameType string, previousRaw, nextRaw []byte) error {
	key, ok := immutableRatingRosterStateKey(gameType)
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
		return errRatingRosterImmutable
	}
	return nil
}

func immutableRatingRosterStateKey(gameType string) (string, bool) {
	switch gameType {
	case "od":
		return "teams", true
	case "ksi":
		return "participants", true
	default:
		return "", false
	}
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

const maxPatchArrayIndex = 4096

func parseJSONPatchPath(parts []json.RawMessage) ([]jsonPathSegment, error) {
	if len(parts) == 0 {
		return nil, errors.New("empty patch path")
	}
	path := make([]jsonPathSegment, 0, len(parts))
	for _, raw := range parts {
		var key string
		if err := json.Unmarshal(raw, &key); err == nil {
			if key == "" {
				return nil, errors.New("empty patch path key")
			}
			path = append(path, jsonPathSegment{key: key})
			continue
		}

		var number json.Number
		if err := json.Unmarshal(raw, &number); err != nil {
			return nil, errors.New("patch path segment must be string or non-negative integer")
		}
		index64, err := strconv.ParseInt(number.String(), 10, 0)
		if err != nil || index64 < 0 {
			return nil, errors.New("patch path index must be a non-negative integer")
		}
		if index64 > maxPatchArrayIndex {
			return nil, fmt.Errorf("patch path index exceeds limit (%d)", maxPatchArrayIndex)
		}
		path = append(path, jsonPathSegment{index: int(index64), isIndex: true})
	}
	return path, nil
}

func decodePatchValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, errors.New("missing patch value")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func applyJSONSet(root any, path []jsonPathSegment, value any) (any, error) {
	if len(path) == 0 {
		return value, nil
	}

	seg := path[0]
	if seg.isIndex {
		var arr []any
		switch current := root.(type) {
		case nil:
			arr = []any{}
		case []any:
			arr = current
		default:
			return nil, errors.New("patch path crosses non-array value")
		}
		for len(arr) <= seg.index {
			arr = append(arr, nil)
		}
		next, err := applyJSONSet(arr[seg.index], path[1:], value)
		if err != nil {
			return nil, err
		}
		arr[seg.index] = next
		return arr, nil
	}

	var obj map[string]any
	switch current := root.(type) {
	case nil:
		obj = map[string]any{}
	case map[string]any:
		obj = current
	default:
		return nil, errors.New("patch path crosses non-object value")
	}
	next, err := applyJSONSet(obj[seg.key], path[1:], value)
	if err != nil {
		return nil, err
	}
	obj[seg.key] = next
	return obj, nil
}

func (s *server) handleScopedGameScheme(w http.ResponseWriter, r *http.Request, scope festScope) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeFestRead(w, r, scope.FestID) {
		return
	}
	var schemeJSON string
	err := s.db.QueryRowContext(r.Context(), `
	select scheme_json from games where fest_id = ? and id = ?`, scope.FestID, scope.GameID).Scan(&schemeJSON)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if schemeJSON == "" {
		schemeJSON = "{}"
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(schemeJSON))
}

func (s *server) handleScopedFest(w http.ResponseWriter, r *http.Request, festID int64) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeFestRead(w, r, festID) {
		return
	}
	gameID, err := defaultGameID(r.Context(), s.db, festID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := s.festViewBytes(festID, gameID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}

func (s *server) handleScopedGame(w http.ResponseWriter, r *http.Request, scope festScope) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeFestRead(w, r, scope.FestID) {
		return
	}
	data, err := s.festViewBytes(scope.FestID, scope.GameID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}

func (s *server) handleScopedVenues(w http.ResponseWriter, r *http.Request, festID int64, sub []string) {
	if len(sub) == 0 {
		switch r.Method {
		case http.MethodGet:
			if !s.authorizeFestRead(w, r, festID) {
				return
			}
			s.mu.RLock()
			venues, err := s.loadVenuesLocked(festID)
			s.mu.RUnlock()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSONValue(w, venues)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(sub) != 1 {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.requireFestAdmin(w, r, festID); !ok {
		return
	}
	number, err := strconv.Atoi(sub[0])
	if err != nil || number <= 0 {
		http.Error(w, "bad venue number", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	var req venueUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	venues, revision, err := s.updateVenue(r.Context(), festID, number, req.Title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, _ := json.Marshal(venues)
	s.broadcastState(festID, fmt.Sprintf("venues:%d", festID), revision, data)
	writeJSON(w, data)
}

// stageMatches is one stage's full match views in the bulk all-stages response.
type stageMatches struct {
	Code    string      `json:"code"`
	Matches []MatchView `json:"matches"`
}

// handleScopedStages routes /api/fest/{tid}/games/{gid}/stages/...
//
//	/stages/{code}/matches → every full MatchView for one stage (a batch
//	                         replacement for the N parallel /matches/{code} fetches).
//	/stages/matches        → every stage's full MatchViews in one response, so the
//	                         bracket page can prefetch the whole game in a single
//	                         request instead of one per stage.
func (s *server) handleScopedStages(w http.ResponseWriter, r *http.Request, scope festScope, sub []string) {
	// All-stages bulk form: /stages/matches (no stage code).
	if len(sub) == 1 && sub[0] == "matches" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.authorizeFestRead(w, r, scope.FestID) {
			return
		}
		stages, err := s.loadAllStageMatchViews(r.Context(), scope)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONValue(w, stages)
		return
	}
	if len(sub) != 2 || sub[0] == "" || sub[1] != "matches" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeFestRead(w, r, scope.FestID) {
		return
	}
	stageCode := sub[0]
	matches, err := s.loadStageMatchViews(r.Context(), scope, stageCode)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONValue(w, matches)
}

// loadAllStageMatchViews returns every stage's full match views for the game in
// one pass, stages ordered by position, matches ordered within each. Empty
// stages (e.g. reseed) are omitted. Takes the read lock once for the whole set.
func (s *server) loadAllStageMatchViews(ctx context.Context, scope festScope) ([]stageMatches, error) {
	rows, err := s.db.QueryContext(ctx, `
select st.code, m.code
from matches m
join stages st on st.id = m.stage_id
where m.fest_id = ? and m.game_id = ?
order by st.position, st.id, m.position, m.id`, scope.FestID, scope.GameID)
	if err != nil {
		return nil, err
	}
	type pair struct{ stageCode, matchCode string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.stageCode, &p.matchCode); err != nil {
			_ = rows.Close()
			return nil, err
		}
		pairs = append(pairs, p)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]stageMatches, 0)
	byCode := map[string]int{} // stage code -> index in out, preserving order
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range pairs {
		mscope, err := s.verifyMatchInScope(ctx, scope, p.matchCode)
		if err != nil {
			if errors.Is(err, errMatchNotFound) {
				continue
			}
			return nil, err
		}
		view, err := s.loadScopedMatchViewLocked(mscope)
		if err != nil {
			return nil, err
		}
		view.Seq = s.currentStateSeq(matchScopeKey(mscope))
		idx, ok := byCode[p.stageCode]
		if !ok {
			idx = len(out)
			byCode[p.stageCode] = idx
			out = append(out, stageMatches{Code: p.stageCode})
		}
		out[idx].Matches = append(out[idx].Matches, view)
	}
	return out, nil
}

func (s *server) loadStageMatchViews(ctx context.Context, scope festScope, stageCode string) ([]MatchView, error) {
	rows, err := s.db.QueryContext(ctx, `
select m.code
from matches m
join stages st on st.id = m.stage_id
where m.fest_id = ? and m.game_id = ? and st.code = ?
order by m.position, m.id`, scope.FestID, scope.GameID, stageCode)
	if err != nil {
		return nil, err
	}
	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			_ = rows.Close()
			return nil, err
		}
		codes = append(codes, code)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(codes) == 0 {
		// Empty stage or unknown stage code; let the caller distinguish via
		// the more specific error from verifyMatchInScope if needed. An empty
		// result is fine: client renders no tables.
		return []MatchView{}, nil
	}
	views := make([]MatchView, 0, len(codes))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, code := range codes {
		mscope, err := s.verifyMatchInScope(ctx, scope, code)
		if err != nil {
			if errors.Is(err, errMatchNotFound) {
				continue
			}
			return nil, err
		}
		view, err := s.loadScopedMatchViewLocked(mscope)
		if err != nil {
			return nil, err
		}
		view.Seq = s.currentStateSeq(matchScopeKey(mscope))
		views = append(views, view)
	}
	return views, nil
}

func (s *server) handleScopedMatches(w http.ResponseWriter, r *http.Request, scope festScope, sub []string) {
	if len(sub) == 0 || len(sub) > 2 {
		http.NotFound(w, r)
		return
	}
	code := sub[0]
	if code == "" {
		http.NotFound(w, r)
		return
	}
	suffix := ""
	if len(sub) > 1 {
		suffix = sub[1]
	}
	switch suffix {
	case "":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.authorizeFestRead(w, r, scope.FestID) {
			return
		}
		mscope, err := s.verifyMatchInScope(r.Context(), scope, code)
		if err != nil {
			if errors.Is(err, errMatchNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.mu.RLock()
		view, err := s.loadScopedMatchViewLocked(mscope)
		s.mu.RUnlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		view.Seq = s.currentStateSeq(matchScopeKey(mscope))
		writeJSONValue(w, view)
	case "update":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := s.requireFestTableEditor(w, r, scope.FestID); !ok {
			return
		}
		mscope, err := s.verifyMatchInScope(r.Context(), scope, code)
		if err != nil {
			if errors.Is(err, errMatchNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()
		var req updateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		// A request may carry a batch of edits to apply atomically (range
		// clear/fill); otherwise the request itself is the single edit.
		reqs := req.Edits
		if len(reqs) == 0 {
			reqs = []updateRequest{req}
		}
		view, data, ops, cascaded, err := s.applyScopedMatchUpdate(r.Context(), mscope, reqs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		view.Seq = s.broadcastMatchView(scope.FestID, mscope, view.Revision, ops, data)
		s.broadcastMatchCascade(scope.FestID, mscope.GameID, cascaded)
		writeJSONValue(w, view)
	case "finish":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := s.requireFestTableEditor(w, r, scope.FestID); !ok {
			return
		}
		mscope, err := s.verifyMatchInScope(r.Context(), scope, code)
		if err != nil {
			if errors.Is(err, errMatchNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()
		var req updateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Finished == nil {
			http.Error(w, "missing finished", http.StatusBadRequest)
			return
		}
		view, data, ops, cascaded, err := s.applyScopedMatchUpdate(r.Context(), mscope, []updateRequest{{Finished: req.Finished}})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		view.Seq = s.broadcastMatchView(scope.FestID, mscope, view.Revision, ops, data)
		s.broadcastMatchCascade(scope.FestID, mscope.GameID, cascaded)
		writeJSONValue(w, view)
	case "venue":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := s.requireFestTableEditor(w, r, scope.FestID); !ok {
			return
		}
		mscope, err := s.verifyMatchInScope(r.Context(), scope, code)
		if err != nil {
			if errors.Is(err, errMatchNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()
		var req matchVenueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		number := req.Number
		if number == 0 {
			number = req.VenueNumber
		}
		view, data, ops, err := s.updateScopedMatchVenue(r.Context(), mscope, number)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		view.Seq = s.broadcastMatchView(scope.FestID, mscope, view.Revision, ops, data)
		writeJSONValue(w, view)
	default:
		http.NotFound(w, r)
	}
}
