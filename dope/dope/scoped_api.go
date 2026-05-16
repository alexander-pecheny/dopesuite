package main

import (
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

type tournamentScope struct {
	TournamentID int64
	GameID       int64
}

func parseScopedPath(path, prefix string) (tournamentScope, string, bool) {
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return tournamentScope{}, "", false
	}
	rest = strings.TrimPrefix(rest, "/")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) < 2 {
		return tournamentScope{}, "", false
	}
	tid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || tid <= 0 {
		return tournamentScope{}, "", false
	}
	if parts[1] != "games" {
		return tournamentScope{}, "", false
	}
	if len(parts) < 3 {
		return tournamentScope{TournamentID: tid}, "", true
	}
	gid, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || gid <= 0 {
		return tournamentScope{}, "", false
	}
	scope := tournamentScope{TournamentID: tid, GameID: gid}
	if len(parts) < 4 {
		return scope, "", true
	}
	return scope, parts[3], true
}

func (s *server) verifyMatchInScope(ctx context.Context, scope tournamentScope, code string) (matchScope, error) {
	row := s.db.QueryRowContext(ctx, `
select id from matches where tournament_id = ? and game_id = ? and code = ?`,
		scope.TournamentID, scope.GameID, code)
	var matchID int64
	if err := row.Scan(&matchID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return matchScope{}, errMatchNotFound
		}
		return matchScope{}, err
	}
	return matchScope{tournamentScope: scope, MatchID: matchID, Code: code}, nil
}

type matchScope struct {
	tournamentScope
	MatchID int64
	Code    string
}

func matchScopeKey(scope matchScope) string {
	return fmt.Sprintf("match:%d:%s", scope.GameID, scope.Code)
}

var errMatchNotFound = errors.New("match not found in this game")

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

func (s *server) tournamentVisibility(ctx context.Context, tournamentID int64) (bool, bool, error) {
	if s.db == nil {
		return false, false, nil
	}
	var isPublic int
	err := s.db.QueryRowContext(ctx, `select is_public from tournaments where id = ?`, tournamentID).Scan(&isPublic)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, isPublic == 1, nil
}

func (s *server) authorizeTournamentRead(w http.ResponseWriter, r *http.Request, tournamentID int64) bool {
	exists, public, err := s.tournamentVisibility(r.Context(), tournamentID)
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
	allowed, err := s.isOrganizer(r.Context(), tournamentID, user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	if !allowed {
		http.NotFound(w, r)
		return false
	}
	return true
}

func (s *server) requireTournamentOrganizer(w http.ResponseWriter, r *http.Request, tournamentID int64) (sessionUser, bool) {
	if !requireSameOriginUnsafe(w, r) {
		return sessionUser{}, false
	}
	user, ok := s.lookupSession(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return sessionUser{}, false
	}
	allowed, err := s.isOrganizer(r.Context(), tournamentID, user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return sessionUser{}, false
	}
	if allowed {
		return user, true
	}
	exists, _, err := s.tournamentVisibility(r.Context(), tournamentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return sessionUser{}, false
	}
	if !exists {
		http.NotFound(w, r)
		return sessionUser{}, false
	}
	http.Error(w, "forbidden", http.StatusForbidden)
	return sessionUser{}, false
}

func (s *server) authorizeHostPresence(w http.ResponseWriter, r *http.Request, tournamentID int64) bool {
	user, ok := s.lookupSession(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	allowed, err := s.isOrganizer(r.Context(), tournamentID, user.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	if allowed {
		return true
	}
	exists, _, err := s.tournamentVisibility(r.Context(), tournamentID)
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

// /api/tournament/{tid}
// /api/tournament/{tid}/venues
// /api/tournament/{tid}/venues/{n}
// /api/tournament/{tid}/games/{gid}
// /api/tournament/{tid}/games/{gid}/matches/{code}
// /api/tournament/{tid}/games/{gid}/matches/{code}/update
// /api/tournament/{tid}/games/{gid}/matches/{code}/finish
// /api/tournament/{tid}/games/{gid}/matches/{code}/venue
func (s *server) handleScopedAPI(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/tournament/")
	if rest == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	tid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || tid <= 0 {
		http.NotFound(w, r)
		return
	}

	if len(parts) == 1 {
		s.handleScopedTournament(w, r, tid)
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
		gid, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || gid <= 0 {
			http.NotFound(w, r)
			return
		}
		scope := tournamentScope{TournamentID: tid, GameID: gid}
		if len(parts) == 3 {
			s.handleScopedGame(w, r, scope)
			return
		}
		switch parts[3] {
		case "matches":
			s.handleScopedMatches(w, r, scope, parts[4:])
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
		}
	}
	http.NotFound(w, r)
}

func (s *server) handleHostPresence(w http.ResponseWriter, r *http.Request, tournamentID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.requireTournamentOrganizer(w, r, tournamentID)
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
	s.broadcastHostPresence(hostPresenceEvent{tournamentID: tournamentID, data: data})
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

func (s *server) handleScopedGameState(w http.ResponseWriter, r *http.Request, scope tournamentScope) {
	switch r.Method {
	case http.MethodGet:
		if !s.authorizeTournamentRead(w, r, scope.TournamentID) {
			return
		}
		var stateJSON string
		err := s.db.QueryRowContext(r.Context(), `
	select state_json from games where tournament_id = ? and id = ?`, scope.TournamentID, scope.GameID).Scan(&stateJSON)
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
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(stateJSON))
	case http.MethodPut:
		if _, ok := s.requireTournamentOrganizer(w, r, scope.TournamentID); !ok {
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
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.broadcastState(scope.TournamentID, fmt.Sprintf("game-state:%d", scope.GameID), revision, raw)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(raw)
	case http.MethodPatch:
		if _, ok := s.requireTournamentOrganizer(w, r, scope.TournamentID); !ok {
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
		s.broadcastState(scope.TournamentID, fmt.Sprintf("game-state:%d", scope.GameID), revision, next)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(next)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) replaceGameState(ctx context.Context, scope tournamentScope, raw []byte) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
update games set state_json = ?, updated_at = ? where tournament_id = ? and id = ?`,
		string(raw), utcNow(), scope.TournamentID, scope.GameID)
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
	revision, err := bumpTournamentRevisionTx(ctx, tx, scope.TournamentID, "game:state", string(raw))
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return revision, nil
}

func (s *server) patchGameState(ctx context.Context, scope tournamentScope, req gameStatePatchRequest, payload string) ([]byte, int64, error) {
	if len(req.Ops) == 0 {
		return nil, 0, errors.New("missing patch ops")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()

	var stateJSON string
	if err := tx.QueryRowContext(ctx, `
select state_json from games where tournament_id = ? and id = ?`,
		scope.TournamentID, scope.GameID).Scan(&stateJSON); err != nil {
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
update games set state_json = ?, updated_at = ? where tournament_id = ? and id = ?`,
		string(next), utcNow(), scope.TournamentID, scope.GameID)
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
	revision, err := bumpTournamentRevisionTx(ctx, tx, scope.TournamentID, "game:state-patch", payload)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return next, revision, nil
}

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

func (s *server) handleScopedGameScheme(w http.ResponseWriter, r *http.Request, scope tournamentScope) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeTournamentRead(w, r, scope.TournamentID) {
		return
	}
	var schemeJSON string
	err := s.db.QueryRowContext(r.Context(), `
	select scheme_json from games where tournament_id = ? and id = ?`, scope.TournamentID, scope.GameID).Scan(&schemeJSON)
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

func (s *server) handleScopedTournament(w http.ResponseWriter, r *http.Request, tournamentID int64) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeTournamentRead(w, r, tournamentID) {
		return
	}
	gameID, err := defaultGameID(r.Context(), s.db, tournamentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.RLock()
	view, err := s.loadTournamentViewLocked(tournamentID, gameID)
	s.mu.RUnlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONValue(w, view)
}

func (s *server) handleScopedGame(w http.ResponseWriter, r *http.Request, scope tournamentScope) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeTournamentRead(w, r, scope.TournamentID) {
		return
	}
	s.mu.RLock()
	view, err := s.loadTournamentViewLocked(scope.TournamentID, scope.GameID)
	s.mu.RUnlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONValue(w, view)
}

func (s *server) handleScopedVenues(w http.ResponseWriter, r *http.Request, tournamentID int64, sub []string) {
	if len(sub) == 0 {
		switch r.Method {
		case http.MethodGet:
			if !s.authorizeTournamentRead(w, r, tournamentID) {
				return
			}
			s.mu.RLock()
			venues, err := s.loadVenuesLocked(tournamentID)
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
	if _, ok := s.requireTournamentOrganizer(w, r, tournamentID); !ok {
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
	venues, revision, err := s.updateVenue(tournamentID, number, req.Title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, _ := json.Marshal(venues)
	s.broadcastState(tournamentID, fmt.Sprintf("venues:%d", tournamentID), revision, data)
	writeJSON(w, data)
}

func (s *server) handleScopedMatches(w http.ResponseWriter, r *http.Request, scope tournamentScope, sub []string) {
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
		if !s.authorizeTournamentRead(w, r, scope.TournamentID) {
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
		writeJSONValue(w, view)
	case "update":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := s.requireTournamentOrganizer(w, r, scope.TournamentID); !ok {
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
		view, data, err := s.applyScopedMatchUpdate(mscope, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.broadcastState(scope.TournamentID, matchScopeKey(mscope), view.Revision, data)
		writeJSON(w, data)
	case "finish":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := s.requireTournamentOrganizer(w, r, scope.TournamentID); !ok {
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
		view, data, err := s.applyScopedMatchUpdate(mscope, updateRequest{Finished: req.Finished})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.broadcastState(scope.TournamentID, matchScopeKey(mscope), view.Revision, data)
		writeJSON(w, data)
	case "venue":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := s.requireTournamentOrganizer(w, r, scope.TournamentID); !ok {
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
		view, data, err := s.updateScopedMatchVenue(mscope, number)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.broadcastState(scope.TournamentID, matchScopeKey(mscope), view.Revision, data)
		writeJSON(w, data)
	default:
		http.NotFound(w, r)
	}
}
