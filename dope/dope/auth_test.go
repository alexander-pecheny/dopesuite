package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newAuthTestServer(t *testing.T) *server {
	t.Helper()
	db, err := openFestDB(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	createDefaultFestFixture(t, db, defaultMatch())
	return &server{db: db, assets: staticFiles, subscribers: make(map[int64]map[chan event]bool)}
}

func systemUserID(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`select id from users where is_system = 1`).Scan(&id); err != nil {
		t.Fatalf("system user: %v", err)
	}
	return id
}

func botConsumeRegister(t *testing.T, db *sql.DB, code string, tgUserID int64, tgUsername string) {
	t.Helper()
	res, err := db.Exec(`
update telegram_login_codes
set telegram_user_id = ?, telegram_username = ?, consumed_at = ?
where code = ? and kind = 'register' and consumed_at is null`,
		tgUserID, tgUsername, time.Now().UTC().Format(time.RFC3339), code)
	if err != nil {
		t.Fatalf("bot consume: %v", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		t.Fatalf("bot consume rows = %d, want 1", n)
	}
}

func botIssueLoginCode(t *testing.T, db *sql.DB, userID, tgUserID int64, tgUsername string) string {
	t.Helper()
	code, err := newTelegramLoginCode()
	if err != nil {
		t.Fatalf("new code: %v", err)
	}
	now := time.Now().UTC()
	_, err = db.Exec(`
insert into telegram_login_codes(code, kind, user_id, telegram_user_id, telegram_username, created_at, expires_at)
values(?, 'login', ?, ?, ?, ?, ?)`,
		code, userID, tgUserID, tgUsername, now.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("issue login: %v", err)
	}
	return code
}

func isShortLoginCode(s string) bool {
	if len(s) != telegramLoginCodeLen {
		return false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func sessionCookieFromHeader(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, raw := range w.Result().Header.Values("Set-Cookie") {
		req := http.Request{Header: http.Header{"Cookie": []string{raw}}}
		if c, err := req.Cookie(sessionCookieName); err == nil && c.Value != "" {
			return c.Value
		}
	}
	t.Fatalf("no session cookie in response")
	return ""
}

func createTestSession(t *testing.T, srv *server, userID int64) string {
	t.Helper()
	tx, err := srv.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin session tx: %v", err)
	}
	defer tx.Rollback()
	token, err := createSessionTx(context.Background(), tx, userID, time.Now().UTC())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit session: %v", err)
	}
	return token
}

func decodeJSON[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	return out
}

func TestProfilePageAndLogout(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 0, ?, ?)`, "profile_user", now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()
	cookie := createTestSession(t, srv, userID)

	hostReq := httptest.NewRequest(http.MethodGet, "/host", nil)
	hostReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	hostResp := httptest.NewRecorder()
	srv.handleHostLanding(hostResp, hostReq)
	if hostResp.Code != http.StatusOK {
		t.Fatalf("host: status %d, body %s", hostResp.Code, hostResp.Body.String())
	}
	if body := hostResp.Body.String(); !strings.Contains(body, `href="/profile"`) || !strings.Contains(body, "profile_user") {
		t.Fatalf("host page missing profile link/user: %s", body)
	}

	profileReq := httptest.NewRequest(http.MethodGet, "/profile", nil)
	profileReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	profileResp := httptest.NewRecorder()
	srv.handleProfilePage(profileResp, profileReq)
	if profileResp.Code != http.StatusOK {
		t.Fatalf("profile: status %d, body %s", profileResp.Code, profileResp.Body.String())
	}
	if body := profileResp.Body.String(); !strings.Contains(body, `action="/profile/logout"`) || !strings.Contains(body, "Разлогиниться") {
		t.Fatalf("profile page missing logout form: %s", body)
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/profile/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	logoutResp := httptest.NewRecorder()
	srv.handleProfileLogout(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusSeeOther {
		t.Fatalf("profile logout status = %d, want 303", logoutResp.Code)
	}
	if got := logoutResp.Header().Get("Location"); got != "/host" {
		t.Fatalf("profile logout location = %q, want /host", got)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	meResp := httptest.NewRecorder()
	srv.handleAuthMe(meResp, meReq)
	if meResp.Code != http.StatusUnauthorized {
		t.Fatalf("me after profile logout = %d, want 401", meResp.Code)
	}
}

func TestProfileSetAndChangePassword(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 0, ?, ?)`, "pw_user", now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()
	cookie := createTestSession(t, srv, userID)

	postPassword := func(body passwordRequest) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewReader(raw))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
		resp := httptest.NewRecorder()
		srv.handleAuthPassword(resp, req)
		return resp
	}

	// Profile page shows the "set password" form when none is set.
	profileReq := httptest.NewRequest(http.MethodGet, "/profile", nil)
	profileReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	profileResp := httptest.NewRecorder()
	srv.handleProfilePage(profileResp, profileReq)
	if body := profileResp.Body.String(); !strings.Contains(body, `data-has-password="0"`) {
		t.Fatalf("profile page should show set-password form: %s", body)
	}

	// Too-short password is rejected.
	if resp := postPassword(passwordRequest{NewPassword: "short"}); resp.Code != http.StatusBadRequest {
		t.Fatalf("short password status = %d, want 400", resp.Code)
	}

	// First set: no current password required.
	if resp := postPassword(passwordRequest{NewPassword: "firstpass1"}); resp.Code != http.StatusNoContent {
		t.Fatalf("set password status = %d, body %s", resp.Code, resp.Body.String())
	}
	var hash sql.NullString
	if err := srv.db.QueryRow(`select password_hash from users where id = ?`, userID).Scan(&hash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if ok, _, _ := verifyPassword(hash.String, "", "firstpass1"); !ok {
		t.Fatalf("stored hash does not verify against new password")
	}

	// Profile page now shows the "change password" form.
	profileResp2 := httptest.NewRecorder()
	srv.handleProfilePage(profileResp2, profileReq)
	if body := profileResp2.Body.String(); !strings.Contains(body, `data-has-password="1"`) {
		t.Fatalf("profile page should show change-password form: %s", body)
	}

	// Changing with a wrong current password fails.
	if resp := postPassword(passwordRequest{CurrentPassword: "wrong", NewPassword: "secondpass2"}); resp.Code != http.StatusUnauthorized {
		t.Fatalf("change with wrong current status = %d, want 401", resp.Code)
	}

	// Changing with the correct current password succeeds.
	if resp := postPassword(passwordRequest{CurrentPassword: "firstpass1", NewPassword: "secondpass2"}); resp.Code != http.StatusNoContent {
		t.Fatalf("change password status = %d, body %s", resp.Code, resp.Body.String())
	}
	if err := srv.db.QueryRow(`select password_hash from users where id = ?`, userID).Scan(&hash); err != nil {
		t.Fatalf("read hash after change: %v", err)
	}
	if ok, _, _ := verifyPassword(hash.String, "", "secondpass2"); !ok {
		t.Fatalf("stored hash does not verify against changed password")
	}
}

func TestProfilePasswordRequiresAuth(t *testing.T) {
	srv := newAuthTestServer(t)
	raw, _ := json.Marshal(passwordRequest{NewPassword: "whatever1"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewReader(raw))
	resp := httptest.NewRecorder()
	srv.handleAuthPassword(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("password without session status = %d, want 401", resp.Code)
	}
}

func TestHostDashboardDeleteButtonsAndGameDelete(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.db))

	dashboardReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d", festID), nil)
	dashboardReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	dashboardResp := httptest.NewRecorder()
	srv.handleHostRouter(dashboardResp, dashboardReq)
	if dashboardResp.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, body %s", dashboardResp.Code, dashboardResp.Body.String())
	}
	body := dashboardResp.Body.String()
	var festSlug string
	if err := srv.db.QueryRow(`select slug from fests where id = ?`, festID).Scan(&festSlug); err != nil {
		t.Fatalf("fest slug: %v", err)
	}
	if !strings.Contains(body, fmt.Sprintf(`/host/fest/%s/game/%d/delete`, festSlug, gameID)) {
		t.Fatalf("dashboard missing game delete form: %s", body)
	}
	if !strings.Contains(body, "Удалить игру?") {
		t.Fatalf("dashboard missing game delete confirmation: %s", body)
	}
	if !strings.Contains(body, "Удалить турнир?") {
		t.Fatalf("dashboard missing fest delete confirmation: %s", body)
	}

	deleteReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/%d/delete", festID, gameID), nil)
	deleteReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	deleteResp := httptest.NewRecorder()
	srv.handleHostRouter(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusSeeOther {
		t.Fatalf("delete game status = %d, body %s", deleteResp.Code, deleteResp.Body.String())
	}
	var games int
	if err := srv.db.QueryRow(`select count(*) from games where fest_id = ?`, festID).Scan(&games); err != nil {
		t.Fatalf("count games: %v", err)
	}
	if games != 0 {
		t.Fatalf("games after delete = %d, want 0", games)
	}
}

func TestHostDashboardAccessAndRoleRoutes(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	creatorID := systemUserID(t, srv.db)
	creatorToken := createTestSession(t, srv, creatorID)
	adminID, adminToken := createAPITestSession(t, srv, "fest-admin")
	hostID, hostToken := createAPITestSession(t, srv, "fest-host")
	bulkHostID, _ := createAPITestSession(t, srv, "bulk-host")
	bulkAdminID, _ := createAPITestSession(t, srv, "bulk-admin")
	bulkRemoveID, _ := createAPITestSession(t, srv, "bulk-remove")
	addAPITestRole(t, srv, festID, adminID, festRoleAdmin)
	addAPITestRole(t, srv, festID, bulkRemoveID, festRoleHost)

	creatorDashboardReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d", festID), nil)
	creatorDashboardReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: creatorToken})
	creatorDashboardResp := httptest.NewRecorder()
	srv.handleHostRouter(creatorDashboardResp, creatorDashboardReq)
	if creatorDashboardResp.Code != http.StatusOK {
		t.Fatalf("creator dashboard status = %d, body %s", creatorDashboardResp.Code, creatorDashboardResp.Body.String())
	}
	if body := creatorDashboardResp.Body.String(); !strings.Contains(body, "Доступ") || !strings.Contains(body, "creator") || !strings.Contains(body, "Массовое действие") {
		t.Fatalf("creator dashboard missing access section: %s", body)
	}

	addHostForm := url.Values{"new_nickname": {"fest-host"}, "new_role": {"host"}, "add_access": {"1"}}
	addHostReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/access", festID), strings.NewReader(addHostForm.Encode()))
	addHostReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addHostReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: creatorToken})
	addHostResp := httptest.NewRecorder()
	srv.handleHostRouter(addHostResp, addHostReq)
	if addHostResp.Code != http.StatusOK {
		t.Fatalf("add host access status = %d, body %s", addHostResp.Code, addHostResp.Body.String())
	}
	if role, err := srv.festUserRole(t.Context(), festID, hostID); err != nil || role != festRoleHost {
		t.Fatalf("host role = %q, err %v; want host", role, err)
	}

	hostDashboardReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d", festID), nil)
	hostDashboardReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: hostToken})
	hostDashboardResp := httptest.NewRecorder()
	srv.handleHostRouter(hostDashboardResp, hostDashboardReq)
	if hostDashboardResp.Code != http.StatusOK {
		t.Fatalf("host dashboard status = %d, body %s", hostDashboardResp.Code, hostDashboardResp.Body.String())
	}
	body := hostDashboardResp.Body.String()
	if !strings.Contains(body, fmt.Sprintf(`/host/fest/fixture-ek/game/%d/`, gameID)) {
		t.Fatalf("host dashboard missing game link: %s", body)
	}
	for _, forbidden := range []string{`name="title"`, "Доступ", "Добавить игру", "Удалить игру"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("host dashboard contains %q: %s", forbidden, body)
		}
	}

	updateFestForm := url.Values{"title": {"Blocked"}}
	updateFestReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d", festID), strings.NewReader(updateFestForm.Encode()))
	updateFestReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	updateFestReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: hostToken})
	updateFestResp := httptest.NewRecorder()
	srv.handleHostRouter(updateFestResp, updateFestReq)
	if updateFestResp.Code != http.StatusForbidden {
		t.Fatalf("host fest update status = %d, want 403", updateFestResp.Code)
	}

	hostNewGameReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/new", festID), nil)
	hostNewGameReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: hostToken})
	hostNewGameResp := httptest.NewRecorder()
	srv.handleHostRouter(hostNewGameResp, hostNewGameReq)
	if hostNewGameResp.Code != http.StatusForbidden {
		t.Fatalf("host new game page status = %d, want 403", hostNewGameResp.Code)
	}

	hostVenuesReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/%d/venues", festID, gameID), nil)
	hostVenuesReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: hostToken})
	hostVenuesResp := httptest.NewRecorder()
	srv.handleHostRouter(hostVenuesResp, hostVenuesReq)
	if hostVenuesResp.Code != http.StatusOK {
		t.Fatalf("host venues page status = %d, body %s", hostVenuesResp.Code, hostVenuesResp.Body.String())
	}

	bulkAccessForm := url.Values{
		"bulk_access": {"1"},
		"bulk_access_lines": {strings.Join([]string{
			"fest-host:admin",
			"bulk-host:host",
			"bulk-admin:admin",
			"bulk-remove:remove",
		}, "\n")},
	}
	bulkAccessReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/access", festID), strings.NewReader(bulkAccessForm.Encode()))
	bulkAccessReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bulkAccessReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: creatorToken})
	bulkAccessResp := httptest.NewRecorder()
	srv.handleHostRouter(bulkAccessResp, bulkAccessReq)
	if bulkAccessResp.Code != http.StatusOK {
		t.Fatalf("bulk access status = %d, body %s", bulkAccessResp.Code, bulkAccessResp.Body.String())
	}
	if body := bulkAccessResp.Body.String(); !strings.Contains(body, "Массовое действие выполнено") {
		t.Fatalf("bulk access response missing notice: %s", body)
	}
	for _, tc := range []struct {
		name   string
		userID int64
		want   string
	}{
		{name: "changed host", userID: hostID, want: festRoleAdmin},
		{name: "added host", userID: bulkHostID, want: festRoleHost},
		{name: "added admin", userID: bulkAdminID, want: festRoleAdmin},
		{name: "removed", userID: bulkRemoveID, want: ""},
	} {
		if role, err := srv.festUserRole(t.Context(), festID, tc.userID); err != nil || role != tc.want {
			t.Fatalf("%s role = %q, err %v; want %q", tc.name, role, err, tc.want)
		}
	}

	adminDeleteReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/delete", festID), nil)
	adminDeleteReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: adminToken})
	adminDeleteResp := httptest.NewRecorder()
	srv.handleHostRouter(adminDeleteResp, adminDeleteReq)
	if adminDeleteResp.Code != http.StatusForbidden {
		t.Fatalf("admin delete fest status = %d, want 403", adminDeleteResp.Code)
	}

	adminRemoveCreator := url.Values{
		fmt.Sprintf("delete_%d", creatorID): {"1"},
	}
	adminAccessReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/access", festID), strings.NewReader(adminRemoveCreator.Encode()))
	adminAccessReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	adminAccessReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: adminToken})
	adminAccessResp := httptest.NewRecorder()
	srv.handleHostRouter(adminAccessResp, adminAccessReq)
	if adminAccessResp.Code != http.StatusOK {
		t.Fatalf("admin access save status = %d, body %s", adminAccessResp.Code, adminAccessResp.Body.String())
	}
	if !strings.Contains(adminAccessResp.Body.String(), "создателя нельзя") {
		t.Fatalf("admin access response missing creator error: %s", adminAccessResp.Body.String())
	}
	if creatorRole, err := srv.festUserRole(t.Context(), festID, creatorID); err != nil || creatorRole != festRoleCreator {
		t.Fatalf("creator role = %q, err %v; want creator", creatorRole, err)
	}
}

func TestHostCreateGameFlow(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.db))

	newReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/new", festID), nil)
	newReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	newResp := httptest.NewRecorder()
	srv.handleHostRouter(newResp, newReq)
	if newResp.Code != http.StatusOK {
		t.Fatalf("new game page status = %d, body %s", newResp.Code, newResp.Body.String())
	}
	if body := newResp.Body.String(); !strings.Contains(body, "ОД") || !strings.Contains(body, "КСИ") || !strings.Contains(body, "ЭК") {
		t.Fatalf("new game page missing game type radios: %s", body)
	} else if strings.Contains(body, `name="game_type" value="od" checked`) ||
		strings.Contains(body, `name="game_type" value="ksi" checked`) ||
		strings.Contains(body, `name="game_type" value="ek" checked`) {
		t.Fatalf("new game page must not preselect game type: %s", body)
	} else if !strings.Contains(body, `data-game-settings="od" hidden`) ||
		!strings.Contains(body, `data-game-settings="ksi" hidden`) ||
		!strings.Contains(body, `data-game-settings="ek" hidden`) ||
		!strings.Contains(body, `data-game-submit hidden`) {
		t.Fatalf("new game page must hide settings and submit until game type is selected: %s", body)
	}

	postGameForm := func(values url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/new", festID), strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.handleHostRouter(resp, req)
		return resp
	}

	odResp := postGameForm(url.Values{
		"game_type":    {"od"},
		"od_tours":     {"2"},
		"od_questions": {"3"},
	})
	if odResp.Code != http.StatusSeeOther {
		t.Fatalf("create od status = %d, body %s", odResp.Code, odResp.Body.String())
	}
	var odSchemeJSON, odStateJSON string
	if err := srv.db.QueryRow(`
select scheme_json, state_json from games where fest_id = ? and game_type = 'od' order by id desc limit 1`, festID).Scan(&odSchemeJSON, &odStateJSON); err != nil {
		t.Fatalf("load od game: %v", err)
	}
	var odScheme struct {
		TourComp []int `json:"tourComp"`
	}
	if err := json.Unmarshal([]byte(odSchemeJSON), &odScheme); err != nil {
		t.Fatalf("decode od scheme: %v", err)
	}
	if len(odScheme.TourComp) != 2 || odScheme.TourComp[0] != 3 || odScheme.TourComp[1] != 3 {
		t.Fatalf("od tourComp = %#v, want [3 3]", odScheme.TourComp)
	}
	var odState struct {
		Entries   [][]int `json:"entries"`
		Completed []bool  `json:"completed"`
	}
	if err := json.Unmarshal([]byte(odStateJSON), &odState); err != nil {
		t.Fatalf("decode od state: %v", err)
	}
	if len(odState.Entries) != 6 || len(odState.Completed) != 6 {
		t.Fatalf("od state shape = entries %d completed %d, want 6/6", len(odState.Entries), len(odState.Completed))
	}

	ksiResp := postGameForm(url.Values{
		"game_type":  {"ksi"},
		"ksi_themes": {"7"},
	})
	if ksiResp.Code != http.StatusSeeOther {
		t.Fatalf("create ksi status = %d, body %s", ksiResp.Code, ksiResp.Body.String())
	}
	var ksiSchemeJSON, ksiStateJSON string
	if err := srv.db.QueryRow(`
select scheme_json, state_json from games where fest_id = ? and game_type = 'ksi' order by id desc limit 1`, festID).Scan(&ksiSchemeJSON, &ksiStateJSON); err != nil {
		t.Fatalf("load ksi game: %v", err)
	}
	var ksiScheme struct {
		Themes int `json:"themes"`
	}
	if err := json.Unmarshal([]byte(ksiSchemeJSON), &ksiScheme); err != nil {
		t.Fatalf("decode ksi scheme: %v", err)
	}
	var ksiState struct {
		Themes []struct {
			Answers [][]string `json:"answers"`
		} `json:"themes"`
	}
	if err := json.Unmarshal([]byte(ksiStateJSON), &ksiState); err != nil {
		t.Fatalf("decode ksi state: %v", err)
	}
	if ksiScheme.Themes != 7 || len(ksiState.Themes) != 7 {
		t.Fatalf("ksi themes = scheme %d state %d, want 7/7", ksiScheme.Themes, len(ksiState.Themes))
	}

	ekScheme := `{"schemaVersion":2,"slug":"ek-added","title":"Добавленная ЭК","gameType":"ek","stages":[{"code":"r1","title":"Раунд","stage_type":"matches","matches":[{"code":"A","title":"Бой A","participantCount":1,"slots":[{"placeholder":"TBD"}]}]}]}`
	ekResp := postGameForm(url.Values{
		"game_type": {"ek"},
		"ek_scheme": {ekScheme},
	})
	if ekResp.Code != http.StatusSeeOther {
		t.Fatalf("create ek status = %d, body %s", ekResp.Code, ekResp.Body.String())
	}
	var ekGameID int64
	if err := srv.db.QueryRow(`
select id from games where fest_id = ? and game_type = 'ek' and title = 'Добавленная ЭК'`, festID).Scan(&ekGameID); err != nil {
		t.Fatalf("load ek game: %v", err)
	}
	var ekMatches int
	if err := srv.db.QueryRow(`select count(*) from matches where game_id = ?`, ekGameID).Scan(&ekMatches); err != nil {
		t.Fatalf("count ek matches: %v", err)
	}
	if ekMatches != 1 {
		t.Fatalf("ek matches = %d, want 1", ekMatches)
	}
}

func TestHiddenAttributeCSSOverridesLayoutClasses(t *testing.T) {
	css, err := os.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("read styles: %v", err)
	}
	body := string(css)
	if !strings.Contains(body, "[hidden]") || !strings.Contains(body, "display: none !important") {
		t.Fatalf("styles.css must force [hidden] to display none so layout classes do not reveal hidden sections")
	}
}

func TestRegisterRedirectsCompletedUserToHost(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 0, ?, ?)`, "done_user", now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()
	cookie := createTestSession(t, srv, userID)

	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp := httptest.NewRecorder()
	srv.handleRegisterPage(resp, req)
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("register status = %d, want 303", resp.Code)
	}
	if got := resp.Header().Get("Location"); got != "/host" {
		t.Fatalf("register redirect = %q, want /host", got)
	}
}

func TestRegisterFlowHappyPath(t *testing.T) {
	srv := newAuthTestServer(t)
	systemID := systemUserID(t, srv.db)
	invite, err := createInvite(context.Background(), srv.db, systemID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// Step 1: site issues register code.
	startBody, _ := json.Marshal(startRegisterRequest{InviteCode: invite})
	startReq := httptest.NewRequest(http.MethodPost, "/api/auth/register/start", bytes.NewReader(startBody))
	startResp := httptest.NewRecorder()
	srv.handleAuthRegisterStart(startResp, startReq)
	if startResp.Code != http.StatusOK {
		t.Fatalf("start: status %d, body %s", startResp.Code, startResp.Body.String())
	}
	startData := decodeJSON[startRegisterResponse](t, startResp)
	if startData.Code == "" {
		t.Fatalf("start: empty code")
	}

	// Polling before bot consumes returns pending.
	pendingReq := httptest.NewRequest(http.MethodGet, "/api/auth/register/status?code="+startData.Code, nil)
	pendingResp := httptest.NewRecorder()
	srv.handleAuthRegisterStatus(pendingResp, pendingReq)
	if pendingResp.Code != http.StatusOK {
		t.Fatalf("pending status: %d", pendingResp.Code)
	}
	if got := decodeJSON[registerStatusResponse](t, pendingResp).Status; got != "pending" {
		t.Fatalf("status before consume = %q, want pending", got)
	}

	// Step 2: bot consumes the register code.
	botConsumeRegister(t, srv.db, startData.Code, 555, "tg_alice")

	// Step 3: site polls again, gets ready + session cookie + no username yet.
	readyReq := httptest.NewRequest(http.MethodGet, "/api/auth/register/status?code="+startData.Code, nil)
	readyResp := httptest.NewRecorder()
	srv.handleAuthRegisterStatus(readyResp, readyReq)
	if readyResp.Code != http.StatusOK {
		t.Fatalf("ready status: %d, body %s", readyResp.Code, readyResp.Body.String())
	}
	ready := decodeJSON[registerStatusResponse](t, readyResp)
	if ready.Status != "ready" {
		t.Fatalf("status = %q, want ready", ready.Status)
	}
	if ready.Username != nil {
		t.Fatalf("username should be nil after register, got %q", *ready.Username)
	}
	cookie := sessionCookieFromHeader(t, readyResp)

	// Invite is now used.
	var usedBy sql.NullInt64
	if err := srv.db.QueryRow(`select used_by from invites where code = ?`, invite).Scan(&usedBy); err != nil {
		t.Fatalf("invite check: %v", err)
	}
	if !usedBy.Valid {
		t.Fatalf("invite still unused after register")
	}

	// Step 4: set username.
	usernameBody, _ := json.Marshal(usernameRequest{Username: "alice"})
	usernameReq := httptest.NewRequest(http.MethodPost, "/api/auth/username", bytes.NewReader(usernameBody))
	usernameReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	usernameResp := httptest.NewRecorder()
	srv.handleAuthUsername(usernameResp, usernameReq)
	if usernameResp.Code != http.StatusOK {
		t.Fatalf("username: status %d, body %s", usernameResp.Code, usernameResp.Body.String())
	}

	// Username is now immutable: second attempt fails.
	rejectReq := httptest.NewRequest(http.MethodPost, "/api/auth/username", bytes.NewReader(usernameBody))
	rejectReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	rejectResp := httptest.NewRecorder()
	srv.handleAuthUsername(rejectResp, rejectReq)
	if rejectResp.Code != http.StatusConflict {
		t.Fatalf("second username attempt: status %d, want 409", rejectResp.Code)
	}

	// /api/auth/me reflects new state.
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	meResp := httptest.NewRecorder()
	srv.handleAuthMe(meResp, meReq)
	if meResp.Code != http.StatusOK {
		t.Fatalf("me: status %d", meResp.Code)
	}
	me := decodeJSON[meResponse](t, meResp)
	if me.Username == nil || *me.Username != "alice" {
		t.Fatalf("me.username = %v, want alice", me.Username)
	}
}

func TestRegisterRejectsExpiredInvite(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC()
	if _, err := srv.db.Exec(`
insert into invites(code, created_by, created_at, expires_at)
values('OLD', ?, ?, ?)`, systemUserID(t, srv.db), now.Add(-2*time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatalf("seed invite: %v", err)
	}
	body, _ := json.Marshal(startRegisterRequest{InviteCode: "OLD"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register/start", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	srv.handleAuthRegisterStart(resp, req)
	if resp.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", resp.Code)
	}
}

func TestRegisterStatusReportsExpiry(t *testing.T) {
	srv := newAuthTestServer(t)
	invite, err := createInvite(context.Background(), srv.db, systemUserID(t, srv.db))
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	body, _ := json.Marshal(startRegisterRequest{InviteCode: invite})
	startReq := httptest.NewRequest(http.MethodPost, "/api/auth/register/start", bytes.NewReader(body))
	startResp := httptest.NewRecorder()
	srv.handleAuthRegisterStart(startResp, startReq)
	if startResp.Code != http.StatusOK {
		t.Fatalf("start: %d", startResp.Code)
	}
	startData := decodeJSON[startRegisterResponse](t, startResp)

	// Backdate the expiry.
	if _, err := srv.db.Exec(`update telegram_login_codes set expires_at = ? where code = ?`,
		time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), startData.Code); err != nil {
		t.Fatalf("expire: %v", err)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/auth/register/status?code="+startData.Code, nil)
	statusResp := httptest.NewRecorder()
	srv.handleAuthRegisterStatus(statusResp, statusReq)
	if got := decodeJSON[registerStatusResponse](t, statusResp).Status; got != "expired" {
		t.Fatalf("status = %q, want expired", got)
	}
}

func TestLoginFlow(t *testing.T) {
	srv := newAuthTestServer(t)
	systemID := systemUserID(t, srv.db)

	// Pre-existing user already linked to telegram.
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, ?, 0, ?, ?)`, 777, "tg_bob", "bob", now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()

	code := botIssueLoginCode(t, srv.db, userID, 777, "tg_bob")

	body, _ := json.Marshal(loginRequest{Code: code})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	srv.handleAuthLogin(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("login: status %d, body %s", resp.Code, resp.Body.String())
	}
	cookie := sessionCookieFromHeader(t, resp)
	me := decodeJSON[meResponse](t, resp)
	if me.Username == nil || *me.Username != "bob" {
		t.Fatalf("me.username = %v, want bob", me.Username)
	}

	// Re-using the login code fails.
	reuseReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	reuseResp := httptest.NewRecorder()
	srv.handleAuthLogin(reuseResp, reuseReq)
	if reuseResp.Code != http.StatusGone {
		t.Fatalf("login reuse status = %d, want 410", reuseResp.Code)
	}

	// System user cannot log in even if a stale login code exists.
	badCode := botIssueLoginCode(t, srv.db, systemID, 0, "")
	badBody, _ := json.Marshal(loginRequest{Code: badCode})
	badReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(badBody))
	badResp := httptest.NewRecorder()
	srv.handleAuthLogin(badResp, badReq)
	if badResp.Code != http.StatusForbidden {
		t.Fatalf("system login status = %d, want 403", badResp.Code)
	}

	// Logout deletes the session.
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	logoutResp := httptest.NewRecorder()
	srv.handleAuthLogout(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", logoutResp.Code)
	}

	// /api/auth/me without a valid cookie -> 401.
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	meResp := httptest.NewRecorder()
	srv.handleAuthMe(meResp, meReq)
	if meResp.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d, want 401", meResp.Code)
	}
}

func TestLoginStartSendsCodeWhenPasswordIsMissing(t *testing.T) {
	srv := newAuthTestServer(t)
	var sentChatID int64
	var sentText string
	srv.sendTelegram = func(ctx context.Context, chatID int64, text string) error {
		sentChatID = chatID
		sentText = text
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, ?, 0, ?, ?)`, 888, "tg_code", "code_only", now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()

	body, _ := json.Marshal(loginStartRequest{Username: "code_only"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/start", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	srv.handleAuthLoginStart(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("login start: status %d, body %s", resp.Code, resp.Body.String())
	}
	start := decodeJSON[loginStartResponse](t, resp)
	if start.HasPassword {
		t.Fatalf("has_password = true, want false")
	}
	if !start.CodeSent {
		t.Fatalf("code_sent = false, want true")
	}
	if sentChatID != 888 {
		t.Fatalf("sent chat = %d, want 888", sentChatID)
	}

	var code string
	if err := srv.db.QueryRow(`
select code from telegram_login_codes where user_id = ? and kind = 'login'`, userID).Scan(&code); err != nil {
		t.Fatalf("read code: %v", err)
	}
	if code == "" || !strings.Contains(sentText, code) {
		t.Fatalf("telegram text %q does not contain issued code %q", sentText, code)
	}
	if !isShortLoginCode(code) {
		t.Fatalf("login code = %q, want [A-Z0-9]{%d}", code, telegramLoginCodeLen)
	}
	if !strings.Contains(sentText, "<code>"+code+"</code>") {
		t.Fatalf("telegram text %q does not render code as monospace", sentText)
	}

	loginBody, _ := json.Marshal(loginRequest{Code: code})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	loginResp := httptest.NewRecorder()
	srv.handleAuthLogin(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("login with issued code: status %d, body %s", loginResp.Code, loginResp.Body.String())
	}
	_ = sessionCookieFromHeader(t, loginResp)
}

func TestLoginStartPasswordUserRequestsCodeExplicitly(t *testing.T) {
	srv := newAuthTestServer(t)
	var sent int
	srv.sendTelegram = func(ctx context.Context, chatID int64, text string) error {
		sent++
		if chatID != 999 {
			t.Fatalf("sent chat = %d, want 999", chatID)
		}
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	pwHash, err := hashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, password_hash, password_salt, is_system, created_at, updated_at)
values(?, ?, ?, ?, null, 0, ?, ?)`, 999, "tg_pass", "passy", pwHash, now, now); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	body, _ := json.Marshal(loginStartRequest{Username: "passy"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/start", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	srv.handleAuthLoginStart(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("login start: status %d, body %s", resp.Code, resp.Body.String())
	}
	start := decodeJSON[loginStartResponse](t, resp)
	if !start.HasPassword {
		t.Fatalf("has_password = false, want true")
	}
	if start.CodeSent {
		t.Fatalf("code_sent = true, want false")
	}
	if sent != 0 {
		t.Fatalf("sent codes = %d, want 0", sent)
	}

	codeBody, _ := json.Marshal(loginStartRequest{Username: "passy", SendCode: true})
	codeReq := httptest.NewRequest(http.MethodPost, "/api/auth/login/start", bytes.NewReader(codeBody))
	codeResp := httptest.NewRecorder()
	srv.handleAuthLoginStart(codeResp, codeReq)
	if codeResp.Code != http.StatusOK {
		t.Fatalf("login code start: status %d, body %s", codeResp.Code, codeResp.Body.String())
	}
	codeStart := decodeJSON[loginStartResponse](t, codeResp)
	if !codeStart.HasPassword || !codeStart.CodeSent {
		t.Fatalf("response = %+v, want password user with sent code", codeStart)
	}
	if sent != 1 {
		t.Fatalf("sent codes = %d, want 1", sent)
	}
}

func TestUsernameValidation(t *testing.T) {
	cases := map[string]bool{
		"alice":                 true,
		"a":                     false,
		"":                      false,
		"with space":            false,
		"со_spaces":             false,
		"привет":                false,
		"alice.bob":             true,
		strings.Repeat("a", 33): false,
	}
	for input, want := range cases {
		if got := validUsername(input); got != want {
			t.Fatalf("validUsername(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestUsernameUniqueness(t *testing.T) {
	srv := newAuthTestServer(t)
	systemID := systemUserID(t, srv.db)

	// Two users registering, both want the same username.
	now := time.Now().UTC().Format(time.RFC3339)
	for _, tg := range []int64{1001, 1002} {
		if _, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, null, 0, ?, ?)`, tg, "tg", now, now); err != nil {
			t.Fatalf("seed user %d: %v", tg, err)
		}
	}
	// Issue login codes & sessions for both.
	var userA, userB int64
	srv.db.QueryRow(`select id from users where telegram_user_id = ?`, int64(1001)).Scan(&userA)
	srv.db.QueryRow(`select id from users where telegram_user_id = ?`, int64(1002)).Scan(&userB)

	tokA, err := func() (string, error) {
		tx, err := srv.db.BeginTx(context.Background(), nil)
		if err != nil {
			return "", err
		}
		defer tx.Rollback()
		token, err := createSessionTx(context.Background(), tx, userA, time.Now().UTC())
		if err != nil {
			return "", err
		}
		return token, tx.Commit()
	}()
	if err != nil {
		t.Fatalf("session A: %v", err)
	}
	tokB, err := func() (string, error) {
		tx, err := srv.db.BeginTx(context.Background(), nil)
		if err != nil {
			return "", err
		}
		defer tx.Rollback()
		token, err := createSessionTx(context.Background(), tx, userB, time.Now().UTC())
		if err != nil {
			return "", err
		}
		return token, tx.Commit()
	}()
	if err != nil {
		t.Fatalf("session B: %v", err)
	}

	body, _ := json.Marshal(usernameRequest{Username: "shared"})

	reqA := httptest.NewRequest(http.MethodPost, "/api/auth/username", bytes.NewReader(body))
	reqA.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokA})
	respA := httptest.NewRecorder()
	srv.handleAuthUsername(respA, reqA)
	if respA.Code != http.StatusOK {
		t.Fatalf("user A username: %d %s", respA.Code, respA.Body.String())
	}

	reqB := httptest.NewRequest(http.MethodPost, "/api/auth/username", bytes.NewReader(body))
	reqB.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokB})
	respB := httptest.NewRecorder()
	srv.handleAuthUsername(respB, reqB)
	if respB.Code != http.StatusConflict {
		t.Fatalf("user B username: %d, want 409", respB.Code)
	}
	_ = systemID
}

func TestSessionSlidingExpiry(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, ?, 0, ?, ?)`, 4242, "tg_x", "x", now, now)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	userID, _ := res.LastInsertId()

	tx, _ := srv.db.BeginTx(context.Background(), nil)
	tok, err := createSessionTx(context.Background(), tx, userID, time.Now().UTC())
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Backdate expiry to one second from now, then call handler -> sliding extension should kick in.
	hash := hashSessionToken(tok)
	soon := time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339)
	if _, err := srv.db.Exec(`update sessions set expires_at = ? where token_hash = ?`, soon, hash); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	resp := httptest.NewRecorder()
	srv.handleAuthMe(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("me: status %d", resp.Code)
	}

	var newExpiry string
	if err := srv.db.QueryRow(`select expires_at from sessions where token_hash = ?`, hash).Scan(&newExpiry); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339, newExpiry)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if time.Until(parsed) < sessionLifetime/2 {
		t.Fatalf("expiry was not slid: %s", newExpiry)
	}
}

func TestRequireSameOriginUnsafeRejectsForwardedHostWithoutTrustedOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://dope.pecheny.me/api/fest/test/presence", nil)
	req.Host = "dope.pecheny.me"
	req.Header.Set("Origin", "https://dope.pecheny.kz")
	req.Header.Set("X-Forwarded-Host", "dope.pecheny.kz")
	resp := httptest.NewRecorder()

	if requireSameOriginUnsafe(resp, req) {
		t.Fatal("same-origin check accepted untrusted forwarded host")
	}
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
}

func TestRequireSameOriginUnsafeAcceptsTrustedOriginHost(t *testing.T) {
	t.Setenv(trustedOriginHostsEnv, "https://dope.pecheny.kz, dope.pecheny.test")
	req := httptest.NewRequest(http.MethodPost, "https://dope.pecheny.me/api/fest/test/presence", nil)
	req.Host = "dope.pecheny.me"
	req.Header.Set("Origin", "https://dope.pecheny.kz")
	resp := httptest.NewRecorder()

	if !requireSameOriginUnsafe(resp, req) {
		t.Fatalf("same-origin check rejected configured origin host: status %d body %q", resp.Code, resp.Body.String())
	}
}

func TestRequireSameOriginUnsafeRejectsMismatchedForwardedHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://dope.pecheny.me/api/fest/test/presence", nil)
	req.Host = "dope.pecheny.me"
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("X-Forwarded-Host", "dope.pecheny.kz")
	resp := httptest.NewRecorder()

	if requireSameOriginUnsafe(resp, req) {
		t.Fatal("same-origin check accepted mismatched forwarded host")
	}
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
}

func TestPasswordHashIsBcryptAndVerifies(t *testing.T) {
	hash, err := hashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("hash %q is not bcrypt", hash)
	}
	ok, upgraded, err := verifyPassword(hash, "", "hunter2")
	if err != nil || !ok {
		t.Fatalf("verify ok = %v err = %v", ok, err)
	}
	if upgraded != "" {
		t.Fatalf("bcrypt hash should not request upgrade, got %q", upgraded)
	}
	ok, _, _ = verifyPassword(hash, "", "wrong")
	if ok {
		t.Fatalf("verify with wrong password returned ok")
	}
}

func TestLegacySHA256PasswordVerifiesAndUpgradesToBcrypt(t *testing.T) {
	salt := "legacy-salt"
	legacy := legacySHA256Password("hunter2", salt)
	if len(legacy) != 64 {
		t.Fatalf("expected hex sha256, got %q", legacy)
	}
	ok, upgraded, err := verifyPassword(legacy, salt, "hunter2")
	if err != nil || !ok {
		t.Fatalf("legacy verify ok = %v err = %v", ok, err)
	}
	if !strings.HasPrefix(upgraded, "$2") {
		t.Fatalf("expected bcrypt upgrade, got %q", upgraded)
	}
	ok, _, _ = verifyPassword(legacy, salt, "wrong")
	if ok {
		t.Fatalf("legacy verify accepted wrong password")
	}
}
