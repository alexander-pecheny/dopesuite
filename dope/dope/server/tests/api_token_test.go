package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	dopeserver "dope/dope/server"

	"pecheny.me/dopecore/session"
)

// bearerRequest sends a JSON request through the /api table with an API
// token, as an agent does.
func bearerRequest(t *testing.T, srv *dopeserver.Server, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp := httptest.NewRecorder()
	srv.HandleScopedAPI(resp, req)
	return resp
}

func decodeInto[T any](t *testing.T, resp *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", resp.Body.String(), err)
	}
	return out
}

func mintToken(t *testing.T, srv *dopeserver.Server, cookie string) string {
	t.Helper()
	resp := scopedAPIRequest(t, srv, http.MethodPost, "/api/auth/tokens", map[string]string{"label": "agent"}, cookie)
	if resp.Code != http.StatusOK {
		t.Fatalf("mint token: %d %s", resp.Code, resp.Body.String())
	}
	created := decodeInto[struct {
		Token string `json:"token"`
	}](t, resp)
	if len(created.Token) != 64 {
		t.Fatalf("token = %q", created.Token)
	}
	return created.Token
}

func wantStatus(t *testing.T, resp *httptest.ResponseRecorder, code int, what string) {
	t.Helper()
	if resp.Code != code {
		t.Fatalf("%s: status %d, want %d: %s", what, resp.Code, code, resp.Body.String())
	}
}

// An agent with a token builds a fest from nothing through the API alone:
// the fest, its settings, a game and its settings, access, and the history.
func TestAPITokenBuildsAFest(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	_, cookie := createAPITestSession(t, srv, "organizer")
	createAPITestSession(t, srv, "helper")
	token := mintToken(t, srv, cookie)

	resp := bearerRequest(t, srv, http.MethodPost, "/api/fests", map[string]any{
		"title": "Кубок API", "slug": "api-cup", "start_date": "2026-10-03", "is_public": true,
	}, token)
	wantStatus(t, resp, http.StatusOK, "create fest")
	fest := decodeInto[struct {
		ID    int64  `json:"id"`
		Slug  string `json:"slug"`
		Title string `json:"title"`
		Role  string `json:"role"`
	}](t, resp)
	if fest.Slug != "api-cup" || fest.Role != "creator" {
		t.Fatalf("created fest = %+v", fest)
	}

	resp = bearerRequest(t, srv, http.MethodPost, "/api/fests", map[string]any{"title": "Дубль", "slug": "api-cup"}, token)
	wantStatus(t, resp, http.StatusBadRequest, "a taken slug")

	list := decodeInto[[]struct {
		ID int64 `json:"id"`
	}](t, bearerRequest(t, srv, http.MethodGet, "/api/fests", nil, token))
	found := false
	for _, f := range list {
		found = found || f.ID == fest.ID
	}
	if !found || len(list) != 1 {
		t.Fatalf("GET /api/fests: want only fest %d, a refused create makes none: %+v", fest.ID, list)
	}

	resp = bearerRequest(t, srv, http.MethodPatch, "/api/fest/api-cup/settings", map[string]any{"description": "Описание"}, token)
	wantStatus(t, resp, http.StatusOK, "patch fest")
	patched := decodeInto[struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		IsPublic    bool   `json:"is_public"`
	}](t, resp)
	if patched.Title != "Кубок API" || patched.Description != "Описание" || !patched.IsPublic {
		t.Fatalf("a PATCH must keep the fields it leaves out: %+v", patched)
	}
	resp = bearerRequest(t, srv, http.MethodPatch, "/api/fest/api-cup/settings", map[string]any{"title": " "}, token)
	wantStatus(t, resp, http.StatusBadRequest, "empty title")

	resp = bearerRequest(t, srv, http.MethodPost, "/api/fest/api-cup/games", map[string]any{
		"game_type": "od", "od_tours": 2, "od_questions": 3,
	}, token)
	wantStatus(t, resp, http.StatusOK, "create game")
	g := decodeInto[struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	}](t, resp)
	if g.ID <= 0 || g.Type != "od" {
		t.Fatalf("created game = %+v", g)
	}
	resp = bearerRequest(t, srv, http.MethodPost, "/api/fest/api-cup/games", map[string]any{"game_type": "nope"}, token)
	wantStatus(t, resp, http.StatusBadRequest, "unknown game type")
	resp = bearerRequest(t, srv, http.MethodPost, "/api/fest/api-cup/games", map[string]any{
		"game_type": "brain", "dsl": "[scheme]\nkind: roundrobin\ngroup_size: 4\n",
	}, token)
	wantStatus(t, resp, http.StatusBadRequest, "a scheme the fest cannot seat")
	if msg := resp.Body.String(); strings.Contains(msg, "ошибка сервера") || strings.Contains(msg, "Некорректный запрос") || !strings.ContainsAny(msg, "абвгдеёжзийклмнопрстуфхцчшщыьэюя") {
		t.Fatalf("a scheme refusal must reach the caller as written, got %q", msg)
	}

	gamePath := fmt.Sprintf("/api/fest/api-cup/games/%d", g.ID)
	resp = bearerRequest(t, srv, http.MethodPatch, gamePath+"/settings", map[string]any{"title": "ЧГК", "slug": "chgk"}, token)
	wantStatus(t, resp, http.StatusOK, "patch game")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/fest/api-cup/games/chgk/settings", nil, token), http.StatusOK, "game by its new slug")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/fest/api-cup/games/chgk/state", nil, token), http.StatusOK, "game state")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/fest/api-cup/games/chgk/journal", nil, token), http.StatusOK, "journal")
	wantStatus(t, bearerRequest(t, srv, http.MethodPost, "/api/fest/api-cup/games/chgk/clear", nil, token), http.StatusOK, "clear")

	resp = bearerRequest(t, srv, http.MethodPost, "/api/fest/api-cup/access", map[string]any{
		"changes": []map[string]any{{"user": "helper", "role": "host"}},
	}, token)
	wantStatus(t, resp, http.StatusOK, "grant access")
	members := decodeInto[[]struct {
		Nickname string `json:"nickname"`
		Role     string `json:"role"`
	}](t, resp)
	if len(members) != 2 || members[1].Nickname != "helper" || members[1].Role != "host" {
		t.Fatalf("members = %+v", members)
	}
	resp = bearerRequest(t, srv, http.MethodPost, "/api/fest/api-cup/access", map[string]any{"lines": "nobody:host"}, token)
	wantStatus(t, resp, http.StatusBadRequest, "unknown user")

	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/fest/api-cup/numbers", nil, token), http.StatusOK, "numbers")
	wantStatus(t, bearerRequest(t, srv, http.MethodPost, "/api/fest/api-cup/numbers/auto", nil, token), http.StatusBadRequest, "auto numbers with no teams")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/fest/api-cup/teams", nil, token), http.StatusOK, "teams")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/fest/api-cup/players", nil, token), http.StatusOK, "players")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/fest/api-cup/troikas", nil, token), http.StatusOK, "troikas")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/fest/api-cup/entrants", nil, token), http.StatusOK, "entrants")

	wantStatus(t, bearerRequest(t, srv, http.MethodDelete, "/api/fest/api-cup/games/chgk", nil, token), http.StatusOK, "delete game")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/fest/api-cup/games/chgk/settings", nil, token), http.StatusNotFound, "deleted game")
	wantStatus(t, bearerRequest(t, srv, http.MethodDelete, "/api/fest/api-cup", nil, token), http.StatusOK, "delete fest")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/fest/api-cup/settings", nil, token), http.StatusNotFound, "deleted fest")
}

// The host API keeps the host pages' roles: a host may not manage the fest.
func TestAPITokenKeepsRoles(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	hostID, hostCookie := createAPITestSession(t, srv, "host")
	addAPITestRole(t, srv, festID, hostID, "host")
	token := mintToken(t, srv, hostCookie)

	settings := fmt.Sprintf("/api/fest/%d/settings", festID)
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, settings, nil, token), http.StatusOK, "a host reads the dashboard")
	wantStatus(t, bearerRequest(t, srv, http.MethodPatch, settings, map[string]any{"title": "x"}, token), http.StatusForbidden, "a host edits the fest")
	resp := bearerRequest(t, srv, http.MethodDelete, fmt.Sprintf("/api/fest/%d", festID), nil, token)
	wantStatus(t, resp, http.StatusForbidden, "a host deletes the fest")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, settings, nil, ""), http.StatusUnauthorized, "no credential")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, settings, nil, "0000"), http.StatusUnauthorized, "a made-up token")
}

// A token is the user except for the kill switch: it cannot change the
// password, and changing the password revokes every token.
func TestAPITokenLifecycle(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	_, cookie := createAPITestSession(t, srv, "owner")
	token := mintToken(t, srv, cookie)
	second := mintToken(t, srv, cookie)

	resp := bearerRequest(t, srv, http.MethodGet, "/api/auth/tokens", nil, token)
	wantStatus(t, resp, http.StatusOK, "list tokens")
	tokens := decodeInto[[]struct {
		ID     int64 `json:"id"`
		Active bool  `json:"active"`
	}](t, resp)
	if len(tokens) != 2 || !tokens[0].Active {
		t.Fatalf("tokens = %+v", tokens)
	}

	pw := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewReader([]byte(`{"new_password":"correct horse battery"}`)))
	pw.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.HandleAuthPassword(rec, pw)
	wantStatus(t, rec, http.StatusForbidden, "a token changes the password")

	wantStatus(t, bearerRequest(t, srv, http.MethodDelete, fmt.Sprintf("/api/auth/tokens/%d", tokens[1].ID), nil, token), http.StatusNoContent, "a token revokes itself")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/auth/tokens", nil, token), http.StatusUnauthorized, "a revoked token")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/auth/tokens", nil, second), http.StatusOK, "the other token still works")

	pw = httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewReader([]byte(`{"new_password":"correct horse battery"}`)))
	pw.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	rec = httptest.NewRecorder()
	srv.HandleAuthPassword(rec, pw)
	wantStatus(t, rec, http.StatusNoContent, "change the password")
	wantStatus(t, bearerRequest(t, srv, http.MethodGet, "/api/auth/tokens", nil, second), http.StatusUnauthorized, "a token after the password change")
	wantStatus(t, scopedAPIRequest(t, srv, http.MethodGet, "/api/auth/tokens", nil, cookie), http.StatusOK, "the session that changed it")
}

// The roster pages' forms, through the API: numbers, Flags and troikas.
func TestAPITokenRoster(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	_, cookie := createAPITestSession(t, srv, "owner")
	token := mintToken(t, srv, cookie)
	resp := bearerRequest(t, srv, http.MethodPost, "/api/fests", map[string]any{"title": "Ростер"}, token)
	wantStatus(t, resp, http.StatusOK, "create fest")
	festID := decodeInto[struct {
		ID int64 `json:"id"`
	}](t, resp).ID
	player := func(id int64, first, last string) roster.FestRosterImportPlayer {
		return roster.FestRosterImportPlayer{RatingID: id, FirstName: first, LastName: last}
	}
	teams := []roster.FestRosterImportTeam{
		{RatingID: 1, Name: "Альфа", City: "Минск", Players: []roster.FestRosterImportPlayer{player(11, "Анна", "Иванова"), player(12, "Борис", "Петров")}},
		{RatingID: 2, Name: "Бета", City: "Брест", Players: []roster.FestRosterImportPlayer{player(21, "Вера", "Сидорова"), player(22, "Глеб", "Орлов")}},
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, teams, imports.RosterChoice{}); err != nil {
		t.Fatalf("import roster: %v", err)
	}
	fest := fmt.Sprintf("/api/fest/%d", festID)

	resp = bearerRequest(t, srv, http.MethodPost, fest+"/numbers/auto", nil, token)
	wantStatus(t, resp, http.StatusOK, "auto numbers")
	numbered := decodeInto[[]struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Number int    `json:"number"`
	}](t, resp)
	if len(numbered) != 2 || numbered[0].Number == 0 || numbered[1].Number == 0 {
		t.Fatalf("auto numbers = %+v", numbered)
	}
	byName := map[string]int64{}
	for _, team := range numbered {
		byName[team.Name] = team.ID
	}
	resp = bearerRequest(t, srv, http.MethodPost, fest+"/numbers/assign", map[string]any{
		"assignments": []map[string]any{{"team_id": byName["Бета"], "number": 7}},
	}, token)
	wantStatus(t, resp, http.StatusOK, "assign a number")
	for _, team := range decodeInto[[]struct {
		Name   string `json:"name"`
		Number int    `json:"number"`
	}](t, resp) {
		if team.Name == "Бета" && team.Number != 7 {
			t.Fatalf("Бета number = %d, want 7", team.Number)
		}
	}
	resp = bearerRequest(t, srv, http.MethodPost, fest+"/numbers/assign", map[string]any{
		"assignments": []map[string]any{{"team_id": byName["Бета"], "number": -1}},
	}, token)
	wantStatus(t, resp, http.StatusBadRequest, "number out of range")
	resp = bearerRequest(t, srv, http.MethodPost, fest+"/numbers/match", map[string]any{"text": "3\tАльфа\n4\tБета"}, token)
	wantStatus(t, resp, http.StatusOK, "match numbers")

	resp = bearerRequest(t, srv, http.MethodPatch, fest+"/teams/flags", map[string]any{
		"flags": map[string]string{fmt.Sprint(byName["Альфа"]): "МЮ, Студ"},
	}, token)
	wantStatus(t, resp, http.StatusOK, "flags")
	for _, team := range decodeInto[[]struct {
		Name  string `json:"name"`
		Flags string `json:"flags"`
	}](t, resp) {
		if team.Name == "Альфа" && team.Flags != "МЮ, Студ" {
			t.Fatalf("Альфа flags = %q", team.Flags)
		}
	}
	resp = bearerRequest(t, srv, http.MethodPatch, fest+"/teams/flags", map[string]any{"flags": map[string]string{"999999": "x"}}, token)
	wantStatus(t, resp, http.StatusBadRequest, "flags of a foreign team")

	resp = bearerRequest(t, srv, http.MethodPost, fest+"/troikas", map[string]any{
		"lines": "Сборная: Анна Иванова, Вера Сидорова, Глеб Орлов",
	}, token)
	wantStatus(t, resp, http.StatusOK, "add troikas")
	troikas := decodeInto[[]struct {
		ID      int64    `json:"id"`
		Name    string   `json:"name"`
		Players []string `json:"players"`
	}](t, resp)
	if len(troikas) != 1 || len(troikas[0].Players) != 3 {
		t.Fatalf("troikas = %+v", troikas)
	}
	resp = bearerRequest(t, srv, http.MethodPut, fmt.Sprintf("%s/troikas/%d", fest, troikas[0].ID), map[string]any{
		"name": "Сборная-2", "players": []string{"Анна Иванова", "Борис Петров", "Глеб Орлов"},
	}, token)
	wantStatus(t, resp, http.StatusOK, "edit troika")
	if got := decodeInto[[]struct {
		Name string `json:"name"`
	}](t, resp); got[0].Name != "Сборная-2" {
		t.Fatalf("renamed troika = %+v", got)
	}
	resp = bearerRequest(t, srv, http.MethodGet, fest+"/entrants", nil, token)
	wantStatus(t, resp, http.StatusOK, "entrants")
	if got := decodeInto[[]struct {
		Troika bool `json:"troika"`
	}](t, resp); !slices.ContainsFunc(got, func(e struct {
		Troika bool `json:"troika"`
	}) bool {
		return e.Troika
	}) {
		t.Fatalf("entrants = %+v", got)
	}
	wantStatus(t, bearerRequest(t, srv, http.MethodDelete, fmt.Sprintf("%s/troikas/%d", fest, troikas[0].ID), nil, token), http.StatusOK, "delete troika")

	resp = bearerRequest(t, srv, http.MethodGet, fest+"/players", nil, token)
	wantStatus(t, resp, http.StatusOK, "players")
	players := decodeInto[struct {
		Players   []any       `json:"players"`
		PlayerIDs []apiOption `json:"player_ids"`
		TeamIDs   []apiOption `json:"team_ids"`
	}](t, resp)
	if len(players.Players) != 4 || len(players.PlayerIDs) == 0 || len(players.TeamIDs) != 2 {
		t.Fatalf("players = %+v", players)
	}
	resp = bearerRequest(t, srv, http.MethodPost, fest+"/numbers/assign", map[string]any{
		"assignments": []map[string]any{{"team_id": byName["Альфа"], "number": 0}},
	}, token)
	wantStatus(t, resp, http.StatusOK, "take one team's number away")
	for _, team := range decodeInto[[]struct {
		Name   string `json:"name"`
		Number int    `json:"number"`
	}](t, resp) {
		if want := map[string]int{"Альфа": 0, "Бета": 7}[team.Name]; team.Number != want {
			t.Fatalf("%s number = %d, want %d", team.Name, team.Number, want)
		}
	}

	resp = bearerRequest(t, srv, http.MethodPost, fest+"/games", map[string]any{"game_type": "ksi", "ksi_themes": 5}, token)
	wantStatus(t, resp, http.StatusOK, "create a KSI game to override in")
	gameID := decodeInto[struct {
		ID int64 `json:"id"`
	}](t, resp).ID
	var anna int64
	for _, o := range players.PlayerIDs {
		if strings.Contains(o.Label, "Иванова") {
			anna = o.ID
		}
	}
	override := map[string]any{"player_id": anna, "team_id": byName["Бета"]}
	wantStatus(t, bearerRequest(t, srv, http.MethodPost, fest+"/players/overrides", override, token), http.StatusBadRequest, "an override without games")
	override["game_ids"] = []int64{gameID}
	resp = bearerRequest(t, srv, http.MethodPost, fest+"/players/overrides", override, token)
	wantStatus(t, resp, http.StatusOK, "add an override")
	overrides := decodeInto[struct {
		Overrides []struct {
			SourceTeamID int64   `json:"source_team_id"`
			TeamID       int64   `json:"team_id"`
			GameIDs      []int64 `json:"game_ids"`
		} `json:"overrides"`
	}](t, resp).Overrides
	if len(overrides) != 1 || overrides[0].SourceTeamID != byName["Альфа"] || overrides[0].TeamID != byName["Бета"] {
		t.Fatalf("overrides = %+v", overrides)
	}
	replace := map[string]any{"player_id": anna, "source_team_id": byName["Альфа"], "team_id": byName["Бета"], "game_ids": []int64{}}
	wantStatus(t, bearerRequest(t, srv, http.MethodPut, fest+"/players/overrides", replace, token), http.StatusBadRequest, "a replace that would delete")
	replace["game_ids"] = []int64{gameID}
	wantStatus(t, bearerRequest(t, srv, http.MethodPut, fest+"/players/overrides", replace, token), http.StatusOK, "replace an override")
	resp = bearerRequest(t, srv, http.MethodDelete, fmt.Sprintf("%s/players/overrides?player_id=%d&source_team_id=%d&team_id=%d", fest, anna, byName["Альфа"], byName["Бета"]), nil, token)
	wantStatus(t, resp, http.StatusOK, "delete an override")
	if left := decodeInto[struct {
		Overrides []any `json:"overrides"`
	}](t, resp).Overrides; len(left) != 0 {
		t.Fatalf("overrides after delete = %+v", left)
	}

	resp = bearerRequest(t, srv, http.MethodPost, fest+"/numbers/clear", nil, token)
	wantStatus(t, resp, http.StatusOK, "clear numbers")
}

type apiOption struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}
