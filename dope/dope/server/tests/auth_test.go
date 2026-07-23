package tests

import (
	"bytes"
	"context"
	"database/sql"
	"dope/dope/domain/core"
	"dope/dope/platform/realtime"
	"dope/dope/platform/roles"
	"dope/dope/platform/util"
	dopeserver "dope/dope/server"
	"dope/dope/storage/festaccess"
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

	"pecheny.me/dopecore/authcred"
	"pecheny.me/dopecore/session"
)

func newAuthTestServer(t *testing.T) *dopeserver.Server {
	t.Helper()
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	createDefaultFestFixture(t, db, dopeserver.DefaultMatch())
	return dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
		e.Assets = dopeserver.StaticFiles
	})
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

func sessionCookieFromHeader(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, raw := range w.Result().Header.Values("Set-Cookie") {
		req := http.Request{Header: http.Header{"Cookie": []string{raw}}}
		if c, err := req.Cookie(session.CookieName); err == nil && c.Value != "" {
			return c.Value
		}
	}
	t.Fatalf("no session cookie in response")
	return ""
}

func createTestSession(t *testing.T, srv *dopeserver.Server, userID int64) string {
	t.Helper()
	tx, err := srv.Eng().DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin session tx: %v", err)
	}
	defer tx.Rollback()
	token, err := dopeserver.CreateSessionTx(context.Background(), tx, userID, time.Now().UTC())
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
	res, err := srv.Eng().DB.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 0, ?, ?)`, "profile_user", now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()
	cookie := createTestSession(t, srv, userID)

	hostReq := httptest.NewRequest(http.MethodGet, "/host", nil)
	hostReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	hostResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostLanding(hostResp, hostReq)
	if hostResp.Code != http.StatusOK {
		t.Fatalf("host: status %d, body %s", hostResp.Code, hostResp.Body.String())
	}
	// The profile link was folded into the ☰ appearance menu (loaded via
	// menu.js, which fetches /api/auth/me), so it is no longer in the
	// server HTML. The username still appears in the title, and the menu script
	// must be present to reach the profile.
	if body := hostResp.Body.String(); !strings.Contains(body, "profile_user") || !strings.Contains(body, "/static/menu.js") {
		t.Fatalf("host page missing username/appearance menu: %s", body)
	}

	profileReq := httptest.NewRequest(http.MethodGet, "/profile", nil)
	profileReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	profileResp := httptest.NewRecorder()
	srv.HostPageServer().HandleProfilePage(profileResp, profileReq)
	if profileResp.Code != http.StatusOK {
		t.Fatalf("profile: status %d, body %s", profileResp.Code, profileResp.Body.String())
	}
	if body := profileResp.Body.String(); !strings.Contains(body, `action="/profile/logout"`) || !strings.Contains(body, "Разлогиниться") {
		t.Fatalf("profile page missing logout form: %s", body)
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/profile/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	logoutResp := httptest.NewRecorder()
	srv.HostPageServer().HandleProfileLogout(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusSeeOther {
		t.Fatalf("profile logout status = %d, want 303", logoutResp.Code)
	}
	if got := logoutResp.Header().Get("Location"); got != "/host" {
		t.Fatalf("profile logout location = %q, want /host", got)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	meResp := httptest.NewRecorder()
	srv.HandleAuthMe(meResp, meReq)
	if meResp.Code != http.StatusUnauthorized {
		t.Fatalf("me after profile logout = %d, want 401", meResp.Code)
	}
}

func TestProfileSetAndChangePassword(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.Eng().DB.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 0, ?, ?)`, "pw_user", now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()
	cookie := createTestSession(t, srv, userID)

	postPassword := func(body dopeserver.PasswordRequest) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewReader(raw))
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
		resp := httptest.NewRecorder()
		srv.HandleAuthPassword(resp, req)
		return resp
	}

	// Profile page shows the "set password" form when none is set.
	profileReq := httptest.NewRequest(http.MethodGet, "/profile", nil)
	profileReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	profileResp := httptest.NewRecorder()
	srv.HostPageServer().HandleProfilePage(profileResp, profileReq)
	if body := profileResp.Body.String(); !strings.Contains(body, `data-has-password="0"`) {
		t.Fatalf("profile page should show set-password form: %s", body)
	}

	// Too-short password is rejected.
	if resp := postPassword(dopeserver.PasswordRequest{NewPassword: "short"}); resp.Code != http.StatusBadRequest {
		t.Fatalf("short password status = %d, want 400", resp.Code)
	}

	// First set: no current password required.
	if resp := postPassword(dopeserver.PasswordRequest{NewPassword: "firstpass1"}); resp.Code != http.StatusNoContent {
		t.Fatalf("set password status = %d, body %s", resp.Code, resp.Body.String())
	}
	var hash sql.NullString
	if err := srv.Eng().DB.QueryRow(`select password_hash from users where id = ?`, userID).Scan(&hash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if ok, _, _ := dopeserver.VerifyPassword(hash.String, "", "firstpass1"); !ok {
		t.Fatalf("stored hash does not verify against new password")
	}

	// Profile page now shows the "change password" form.
	profileResp2 := httptest.NewRecorder()
	srv.HostPageServer().HandleProfilePage(profileResp2, profileReq)
	if body := profileResp2.Body.String(); !strings.Contains(body, `data-has-password="1"`) {
		t.Fatalf("profile page should show change-password form: %s", body)
	}

	// Changing with a wrong current password fails.
	if resp := postPassword(dopeserver.PasswordRequest{CurrentPassword: "wrong", NewPassword: "secondpass2"}); resp.Code != http.StatusUnauthorized {
		t.Fatalf("change with wrong current status = %d, want 401", resp.Code)
	}

	// Changing with the correct current password succeeds.
	if resp := postPassword(dopeserver.PasswordRequest{CurrentPassword: "firstpass1", NewPassword: "secondpass2"}); resp.Code != http.StatusNoContent {
		t.Fatalf("change password status = %d, body %s", resp.Code, resp.Body.String())
	}
	if err := srv.Eng().DB.QueryRow(`select password_hash from users where id = ?`, userID).Scan(&hash); err != nil {
		t.Fatalf("read hash after change: %v", err)
	}
	if ok, _, _ := dopeserver.VerifyPassword(hash.String, "", "secondpass2"); !ok {
		t.Fatalf("stored hash does not verify against changed password")
	}
}

func TestProfilePasswordRequiresAuth(t *testing.T) {
	srv := newAuthTestServer(t)
	raw, _ := json.Marshal(dopeserver.PasswordRequest{NewPassword: "whatever1"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewReader(raw))
	resp := httptest.NewRecorder()
	srv.HandleAuthPassword(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("password without session status = %d, want 401", resp.Code)
	}
}

func TestHostDashboardDeleteButtonsAndGameDelete(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))

	dashboardReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d", festID), nil)
	dashboardReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	dashboardResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(dashboardResp, dashboardReq)
	if dashboardResp.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, body %s", dashboardResp.Code, dashboardResp.Body.String())
	}
	body := dashboardResp.Body.String()
	var festSlug string
	if err := srv.Eng().DB.QueryRow(`select slug from fests where id = ?`, festID).Scan(&festSlug); err != nil {
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
	deleteReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	deleteResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusSeeOther {
		t.Fatalf("delete game status = %d, body %s", deleteResp.Code, deleteResp.Body.String())
	}
	var games int
	if err := srv.Eng().DB.QueryRow(`select count(*) from games where fest_id = ?`, festID).Scan(&games); err != nil {
		t.Fatalf("count games: %v", err)
	}
	if games != 0 {
		t.Fatalf("games after delete = %d, want 0", games)
	}
}

func TestHostClearGameResetsToPristine(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))

	var festSlug string
	if err := srv.Eng().DB.QueryRow(`select slug from fests where id = ?`, festID).Scan(&festSlug); err != nil {
		t.Fatalf("fest slug: %v", err)
	}

	// Dashboard exposes the clear control before the delete control.
	dashReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d", festID), nil)
	dashReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	dashResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(dashResp, dashReq)
	body := dashResp.Body.String()
	if !strings.Contains(body, fmt.Sprintf(`/host/fest/%s/game/%d/clear`, festSlug, gameID)) {
		t.Fatalf("dashboard missing game clear form: %s", body)
	}
	if !strings.Contains(body, "Очистить игру?") || !strings.Contains(body, ">Очистить<") {
		t.Fatalf("dashboard missing clear button/confirmation: %s", body)
	}

	countByGame := func(label, q string) int {
		t.Helper()
		var n int
		if err := srv.Eng().DB.QueryRow(q, gameID).Scan(&n); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		return n
	}
	themesQ := `select count(*) from matches m where m.game_id = ? and m.state_json != '{}'`
	resultsQ := `select count(*) from match_results r join matches m on m.id = r.match_id where m.game_id = ?`
	assignQ := `select count(*) from game_assignments where game_id = ?`
	matchesQ := `select count(*) from matches where game_id = ?`

	if countByGame("themes", themesQ) == 0 {
		t.Fatalf("fixture expected to have themes before clear")
	}
	if countByGame("results", resultsQ) == 0 {
		t.Fatalf("fixture expected to have match_results before clear")
	}
	var teamsBefore int
	if err := srv.Eng().DB.QueryRow(`select count(*) from teams where fest_id = ?`, festID).Scan(&teamsBefore); err != nil {
		t.Fatalf("teams before: %v", err)
	}

	clearReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/%d/clear", festID, gameID), nil)
	clearReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	clearResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(clearResp, clearReq)
	if clearResp.Code != http.StatusSeeOther {
		t.Fatalf("clear status = %d, body %s", clearResp.Code, clearResp.Body.String())
	}

	// The game survives with the same id; derived data is gone, the bracket is
	// rebuilt empty, and fest-scoped teams are untouched.
	var status string
	if err := srv.Eng().DB.QueryRow(`select status from games where id = ? and fest_id = ?`, gameID, festID).Scan(&status); err != nil {
		t.Fatalf("game gone after clear: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status after clear = %q, want pending", status)
	}
	if n := countByGame("themes", themesQ); n != 0 {
		t.Fatalf("themes after clear = %d, want 0", n)
	}
	if n := countByGame("results", resultsQ); n != 0 {
		t.Fatalf("match_results after clear = %d, want 0", n)
	}
	if n := countByGame("assignments", assignQ); n != 0 {
		t.Fatalf("game_assignments after clear = %d, want 0", n)
	}
	if n := countByGame("matches", matchesQ); n != 1 {
		t.Fatalf("matches after clear = %d, want 1 (rebuilt from scheme)", n)
	}
	var teamsAfter int
	if err := srv.Eng().DB.QueryRow(`select count(*) from teams where fest_id = ?`, festID).Scan(&teamsAfter); err != nil {
		t.Fatalf("teams after: %v", err)
	}
	if teamsAfter != teamsBefore {
		t.Fatalf("fest teams changed by clear: before %d, after %d", teamsBefore, teamsAfter)
	}
}

func TestHostDashboardAccessAndRoleRoutes(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	creatorID := systemUserID(t, srv.Eng().DB)
	creatorToken := createTestSession(t, srv, creatorID)
	adminID, adminToken := createAPITestSession(t, srv, "fest-admin")
	hostID, hostToken := createAPITestSession(t, srv, "fest-host")
	bulkHostID, _ := createAPITestSession(t, srv, "bulk-host")
	bulkAdminID, _ := createAPITestSession(t, srv, "bulk-admin")
	bulkRemoveID, _ := createAPITestSession(t, srv, "bulk-remove")
	addAPITestRole(t, srv, festID, adminID, roles.Admin)
	addAPITestRole(t, srv, festID, bulkRemoveID, roles.Host)

	creatorDashboardReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d", festID), nil)
	creatorDashboardReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: creatorToken})
	creatorDashboardResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(creatorDashboardResp, creatorDashboardReq)
	if creatorDashboardResp.Code != http.StatusOK {
		t.Fatalf("creator dashboard status = %d, body %s", creatorDashboardResp.Code, creatorDashboardResp.Body.String())
	}
	if body := creatorDashboardResp.Body.String(); !strings.Contains(body, "Доступ") || !strings.Contains(body, "creator") || !strings.Contains(body, "Массовое действие") {
		t.Fatalf("creator dashboard missing access section: %s", body)
	}

	addHostForm := url.Values{"new_nickname": {"fest-host"}, "new_role": {"host"}, "add_access": {"1"}}
	addHostReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/access", festID), strings.NewReader(addHostForm.Encode()))
	addHostReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addHostReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: creatorToken})
	addHostResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(addHostResp, addHostReq)
	if addHostResp.Code != http.StatusOK {
		t.Fatalf("add host access status = %d, body %s", addHostResp.Code, addHostResp.Body.String())
	}
	if role, err := festaccess.FestUserRoleFromQuery(t.Context(), srv.Eng().DB, festID, hostID); err != nil || role != roles.Host {
		t.Fatalf("host role = %q, err %v; want host", role, err)
	}

	hostDashboardReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d", festID), nil)
	hostDashboardReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: hostToken})
	hostDashboardResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(hostDashboardResp, hostDashboardReq)
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
	updateFestReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: hostToken})
	updateFestResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(updateFestResp, updateFestReq)
	if updateFestResp.Code != http.StatusForbidden {
		t.Fatalf("host fest update status = %d, want 403", updateFestResp.Code)
	}

	hostNewGameReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/new", festID), nil)
	hostNewGameReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: hostToken})
	hostNewGameResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(hostNewGameResp, hostNewGameReq)
	if hostNewGameResp.Code != http.StatusForbidden {
		t.Fatalf("host new game page status = %d, want 403", hostNewGameResp.Code)
	}

	hostVenuesReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/%d/venues", festID, gameID), nil)
	hostVenuesReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: hostToken})
	hostVenuesResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(hostVenuesResp, hostVenuesReq)
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
	bulkAccessReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: creatorToken})
	bulkAccessResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(bulkAccessResp, bulkAccessReq)
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
		{name: "changed host", userID: hostID, want: roles.Admin},
		{name: "added host", userID: bulkHostID, want: roles.Host},
		{name: "added admin", userID: bulkAdminID, want: roles.Admin},
		{name: "removed", userID: bulkRemoveID, want: ""},
	} {
		if role, err := festaccess.FestUserRoleFromQuery(t.Context(), srv.Eng().DB, festID, tc.userID); err != nil || role != tc.want {
			t.Fatalf("%s role = %q, err %v; want %q", tc.name, role, err, tc.want)
		}
	}

	adminDeleteReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/delete", festID), nil)
	adminDeleteReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: adminToken})
	adminDeleteResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(adminDeleteResp, adminDeleteReq)
	if adminDeleteResp.Code != http.StatusForbidden {
		t.Fatalf("admin delete fest status = %d, want 403", adminDeleteResp.Code)
	}

	adminRemoveCreator := url.Values{
		fmt.Sprintf("delete_%d", creatorID): {"1"},
	}
	adminAccessReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/access", festID), strings.NewReader(adminRemoveCreator.Encode()))
	adminAccessReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	adminAccessReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: adminToken})
	adminAccessResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(adminAccessResp, adminAccessReq)
	if adminAccessResp.Code != http.StatusOK {
		t.Fatalf("admin access save status = %d, body %s", adminAccessResp.Code, adminAccessResp.Body.String())
	}
	if !strings.Contains(adminAccessResp.Body.String(), "создателя нельзя") {
		t.Fatalf("admin access response missing creator error: %s", adminAccessResp.Body.String())
	}
	if creatorRole, err := festaccess.FestUserRoleFromQuery(t.Context(), srv.Eng().DB, festID, creatorID); err != nil || creatorRole != roles.Creator {
		t.Fatalf("creator role = %q, err %v; want creator", creatorRole, err)
	}
}

// hiddenAttrPresent reports whether the element carrying marker also has the
// bare `hidden` attribute, regardless of attribute order (the typed UI builder
// emits universal props like hidden ahead of the page's data-* attrs).
func hiddenAttrPresent(body, marker string) bool {
	return strings.Contains(body, "hidden "+marker) || strings.Contains(body, marker+" hidden")
}

func TestHostCreateGameFlow(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))

	newReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/new", festID), nil)
	newReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	newResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(newResp, newReq)
	if newResp.Code != http.StatusOK {
		t.Fatalf("new game page status = %d, body %s", newResp.Code, newResp.Body.String())
	}
	if body := newResp.Body.String(); !strings.Contains(body, "ОД") || !strings.Contains(body, "КСИ") || !strings.Contains(body, "ЭК") {
		t.Fatalf("new game page missing game type radios: %s", body)
	} else if strings.Contains(body, `name="game_type" value="od" checked`) ||
		strings.Contains(body, `name="game_type" value="ksi" checked`) ||
		strings.Contains(body, `name="game_type" value="ek" checked`) {
		t.Fatalf("new game page must not preselect game type: %s", body)
	} else if !hiddenAttrPresent(body, `data-game-settings="od"`) ||
		!hiddenAttrPresent(body, `data-game-settings="ksi"`) ||
		!hiddenAttrPresent(body, `data-game-settings="ek"`) ||
		!hiddenAttrPresent(body, "data-game-submit") {
		t.Fatalf("new game page must hide settings and submit until game type is selected: %s", body)
	}

	postGameForm := func(values url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/new", festID), strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
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
	if err := srv.Eng().DB.QueryRow(`
select scheme_json,
       coalesce((select m.state_json from matches m where m.game_id = games.id and m.code = 'main'), '{}')
from games where fest_id = ? and game_type = 'od' order by id desc limit 1`, festID).Scan(&odSchemeJSON, &odStateJSON); err != nil {
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
	if err := srv.Eng().DB.QueryRow(`
select scheme_json,
       coalesce((select m.state_json from matches m where m.game_id = games.id and m.code = 'main'), '{}')
from games where fest_id = ? and game_type = 'ksi' order by id desc limit 1`, festID).Scan(&ksiSchemeJSON, &ksiStateJSON); err != nil {
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
	if err := srv.Eng().DB.QueryRow(`
select id from games where fest_id = ? and game_type = 'ek' and title = 'Добавленная ЭК'`, festID).Scan(&ekGameID); err != nil {
		t.Fatalf("load ek game: %v", err)
	}
	var ekMatches int
	if err := srv.Eng().DB.QueryRow(`select count(*) from matches where game_id = ?`, ekGameID).Scan(&ekMatches); err != nil {
		t.Fatalf("count ek matches: %v", err)
	}
	if ekMatches != 1 {
		t.Fatalf("ek matches = %d, want 1", ekMatches)
	}
}

func TestHiddenAttributeCSSOverridesLayoutClasses(t *testing.T) {
	css, err := os.ReadFile("../../web/assets/static/styles.css")
	if err != nil {
		t.Fatalf("read styles: %v", err)
	}
	body := string(css)
	if !strings.Contains(body, "[hidden]") || !strings.Contains(body, "display: none !important") {
		t.Fatalf("styles.css must force [hidden] to display none so layout classes do not reveal hidden sections")
	}
}

func TestRegisterFlowHappyPath(t *testing.T) {
	srv := newAuthTestServer(t)

	// Telegram handshake -> code.
	startResp := httptest.NewRecorder()
	srv.HandleAuthTgStart(startResp, httptest.NewRequest(http.MethodPost, "/api/auth/tg/start", nil))
	if startResp.Code != http.StatusOK {
		t.Fatalf("start: status %d, body %s", startResp.Code, startResp.Body.String())
	}
	code := decodeJSON[session.StartRegisterResponse](t, startResp).Code
	if code == "" {
		t.Fatalf("start: empty code")
	}

	// Polling before the bot confirms returns pending.
	pendingResp := httptest.NewRecorder()
	srv.HandleAuthTgStatus(pendingResp, httptest.NewRequest(http.MethodGet, "/api/auth/tg/status?code="+code, nil))
	if got := decodeJSON[session.RegisterStatusResponse](t, pendingResp).Status; got != "pending" {
		t.Fatalf("status before confirm = %q, want pending", got)
	}

	// Bot confirms a new telegram account.
	botConsumeRegister(t, srv.Eng().DB, code, 555, "tg_alice")

	// Status now reports choose_username (account not yet created).
	statusResp := httptest.NewRecorder()
	srv.HandleAuthTgStatus(statusResp, httptest.NewRequest(http.MethodGet, "/api/auth/tg/status?code="+code, nil))
	if got := decodeJSON[session.RegisterStatusResponse](t, statusResp).Status; got != "choose_username" {
		t.Fatalf("status = %q, want choose_username", got)
	}

	// Claim the username -> ready + session cookie.
	claimResp := tgClaim(t, srv, code, "alice", "")
	ready := decodeJSON[session.RegisterStatusResponse](t, claimResp)
	if ready.Status != "ready" || ready.Username == nil || *ready.Username != "alice" {
		t.Fatalf("claim = %+v, want ready/alice", ready)
	}
	cookie := sessionCookieFromHeader(t, claimResp)

	// /api/auth/me reflects the new account.
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	meResp := httptest.NewRecorder()
	srv.HandleAuthMe(meResp, meReq)
	me := decodeJSON[dopeserver.MeResponse](t, meResp)
	if me.Username == nil || *me.Username != "alice" {
		t.Fatalf("me.username = %v, want alice", me.Username)
	}
}

// tgClaim POSTs a username claim (optionally with a password) and returns the recorder.
func tgClaim(t *testing.T, srv *dopeserver.Server, code, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code, "username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/tg/claim", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	srv.HandleAuthTgClaim(resp, req)
	return resp
}

func TestRegisterLinkPasswordAccount(t *testing.T) {
	srv := newAuthTestServer(t)
	hash, err := dopeserver.HashPassword("dopevetpass1")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := srv.Eng().DB.Exec(`
insert into users(username, password_hash, password_salt, is_system, created_at, updated_at)
values('dpvet', ?, '', 0, ?, ?)`, hash, now, now); err != nil {
		t.Fatalf("seed pw user: %v", err)
	}

	startResp := httptest.NewRecorder()
	srv.HandleAuthTgStart(startResp, httptest.NewRequest(http.MethodPost, "/api/auth/tg/start", nil))
	code := decodeJSON[session.StartRegisterResponse](t, startResp).Code
	botConsumeRegister(t, srv.Eng().DB, code, 800500, "vt")

	// Claim the password account's name with no password -> password_required.
	if got := decodeJSON[session.RegisterStatusResponse](t, tgClaim(t, srv, code, "dpvet", "")).Status; got != "password_required" {
		t.Fatalf("status = %q, want password_required", got)
	}
	// Wrong password rejected.
	if resp := tgClaim(t, srv, code, "dpvet", "wrong"); resp.Code == http.StatusOK {
		t.Fatalf("wrong password accepted: %d", resp.Code)
	}
	// Correct password links + logs in.
	linked := tgClaim(t, srv, code, "dpvet", "dopevetpass1")
	if got := decodeJSON[session.RegisterStatusResponse](t, linked); got.Status != "ready" || got.Username == nil || *got.Username != "dpvet" {
		t.Fatalf("link = %+v, want ready/dpvet", got)
	}
	var tg sql.NullInt64
	if err := srv.Eng().DB.QueryRow(`select telegram_user_id from users where username = 'dpvet'`).Scan(&tg); err != nil {
		t.Fatalf("check: %v", err)
	}
	if !tg.Valid || tg.Int64 != 800500 {
		t.Fatalf("telegram_user_id = %v, want 800500", tg)
	}
}

func TestTgClaimRejectsSystemAccount(t *testing.T) {
	srv := newAuthTestServer(t)
	hash, err := dopeserver.HashPassword("syspass12345")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := srv.Eng().DB.Exec(`
insert into users(username, password_hash, password_salt, is_system, created_at, updated_at)
values('sys', ?, '', 1, ?, ?)`, hash, now, now); err != nil {
		t.Fatalf("seed system user: %v", err)
	}
	startResp := httptest.NewRecorder()
	srv.HandleAuthTgStart(startResp, httptest.NewRequest(http.MethodPost, "/api/auth/tg/start", nil))
	code := decodeJSON[session.StartRegisterResponse](t, startResp).Code
	botConsumeRegister(t, srv.Eng().DB, code, 700700, "sys_tg")
	// Even with the correct password, a system account is not linkable.
	if got := decodeJSON[session.RegisterStatusResponse](t, tgClaim(t, srv, code, "sys", "syspass12345")).Status; got != "username_taken" {
		t.Fatalf("status = %q, want username_taken (system account not linkable)", got)
	}
}

func TestRegisterRejectsTakenPasswordlessUsername(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := srv.Eng().DB.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(999, 'tg', 'taken', 0, ?, ?)`, now, now); err != nil { // telegram-only, no password
		t.Fatalf("seed user: %v", err)
	}
	startResp := httptest.NewRecorder()
	srv.HandleAuthTgStart(startResp, httptest.NewRequest(http.MethodPost, "/api/auth/tg/start", nil))
	code := decodeJSON[session.StartRegisterResponse](t, startResp).Code
	botConsumeRegister(t, srv.Eng().DB, code, 1001, "newbie")
	if got := decodeJSON[session.RegisterStatusResponse](t, tgClaim(t, srv, code, "taken", "")).Status; got != "username_taken" {
		t.Fatalf("status = %q, want username_taken", got)
	}
}

func TestRegisterStatusReportsExpiry(t *testing.T) {
	srv := newAuthTestServer(t)
	startResp := httptest.NewRecorder()
	srv.HandleAuthTgStart(startResp, httptest.NewRequest(http.MethodPost, "/api/auth/tg/start", nil))
	code := decodeJSON[session.StartRegisterResponse](t, startResp).Code

	if _, err := srv.Eng().DB.Exec(`update telegram_login_codes set expires_at = ? where code = ?`,
		time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), code); err != nil {
		t.Fatalf("expire: %v", err)
	}
	statusResp := httptest.NewRecorder()
	srv.HandleAuthTgStatus(statusResp, httptest.NewRequest(http.MethodGet, "/api/auth/tg/status?code="+code, nil))
	if got := decodeJSON[session.RegisterStatusResponse](t, statusResp).Status; got != "expired" {
		t.Fatalf("status = %q, want expired", got)
	}
}

func TestPasswordLogin(t *testing.T) {
	srv := newAuthTestServer(t)
	hash, err := dopeserver.HashPassword("s3cretpassword")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := srv.Eng().DB.Exec(`
insert into users(username, password_hash, password_salt, is_system, created_at, updated_at)
values('boss', ?, '', 0, ?, ?)`, hash, now, now); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"username": "boss", "password": "s3cretpassword"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login-password", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	srv.HandleAuthLoginPassword(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("login-password: status %d, body %s", resp.Code, resp.Body.String())
	}
	me := decodeJSON[dopeserver.MeResponse](t, resp)
	if me.Username == nil || *me.Username != "boss" {
		t.Fatalf("me = %+v, want boss", me)
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
		if got := util.ValidUsername(input); got != want {
			t.Fatalf("util.ValidUsername(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestUsernameUniqueness(t *testing.T) {
	srv := newAuthTestServer(t)
	systemID := systemUserID(t, srv.Eng().DB)

	// Two users registering, both want the same username.
	now := time.Now().UTC().Format(time.RFC3339)
	for _, tg := range []int64{1001, 1002} {
		if _, err := srv.Eng().DB.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, null, 0, ?, ?)`, tg, "tg", now, now); err != nil {
			t.Fatalf("seed user %d: %v", tg, err)
		}
	}
	// Issue login codes & sessions for both.
	var userA, userB int64
	srv.Eng().DB.QueryRow(`select id from users where telegram_user_id = ?`, int64(1001)).Scan(&userA)
	srv.Eng().DB.QueryRow(`select id from users where telegram_user_id = ?`, int64(1002)).Scan(&userB)

	tokA, err := func() (string, error) {
		tx, err := srv.Eng().DB.BeginTx(context.Background(), nil)
		if err != nil {
			return "", err
		}
		defer tx.Rollback()
		token, err := dopeserver.CreateSessionTx(context.Background(), tx, userA, time.Now().UTC())
		if err != nil {
			return "", err
		}
		return token, tx.Commit()
	}()
	if err != nil {
		t.Fatalf("session A: %v", err)
	}
	tokB, err := func() (string, error) {
		tx, err := srv.Eng().DB.BeginTx(context.Background(), nil)
		if err != nil {
			return "", err
		}
		defer tx.Rollback()
		token, err := dopeserver.CreateSessionTx(context.Background(), tx, userB, time.Now().UTC())
		if err != nil {
			return "", err
		}
		return token, tx.Commit()
	}()
	if err != nil {
		t.Fatalf("session B: %v", err)
	}

	body, _ := json.Marshal(dopeserver.UsernameRequest{Username: "shared"})

	reqA := httptest.NewRequest(http.MethodPost, "/api/auth/username", bytes.NewReader(body))
	reqA.AddCookie(&http.Cookie{Name: session.CookieName, Value: tokA})
	respA := httptest.NewRecorder()
	srv.HandleAuthUsername(respA, reqA)
	if respA.Code != http.StatusOK {
		t.Fatalf("user A username: %d %s", respA.Code, respA.Body.String())
	}

	reqB := httptest.NewRequest(http.MethodPost, "/api/auth/username", bytes.NewReader(body))
	reqB.AddCookie(&http.Cookie{Name: session.CookieName, Value: tokB})
	respB := httptest.NewRecorder()
	srv.HandleAuthUsername(respB, reqB)
	if respB.Code != http.StatusConflict {
		t.Fatalf("user B username: %d, want 409", respB.Code)
	}
	_ = systemID
}

func TestSessionSlidingExpiry(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.Eng().DB.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, ?, 0, ?, ?)`, 4242, "tg_x", "x", now, now)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	userID, _ := res.LastInsertId()

	tx, _ := srv.Eng().DB.BeginTx(context.Background(), nil)
	tok, err := dopeserver.CreateSessionTx(context.Background(), tx, userID, time.Now().UTC())
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Backdate expiry to one second from now, then call handler -> sliding extension should kick in.
	hash := authcred.HashSessionToken(tok)
	soon := time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339)
	if _, err := srv.Eng().DB.Exec(`update sessions set expires_at = ? where token_hash = ?`, soon, hash); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: tok})
	resp := httptest.NewRecorder()
	srv.HandleAuthMe(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("me: status %d", resp.Code)
	}

	var newExpiry string
	if err := srv.Eng().DB.QueryRow(`select expires_at from sessions where token_hash = ?`, hash).Scan(&newExpiry); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339, newExpiry)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if time.Until(parsed) < session.Lifetime/2 {
		t.Fatalf("expiry was not slid: %s", newExpiry)
	}
}

func TestRequireSameOriginUnsafeRejectsForwardedHostWithoutTrustedOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://dope.pecheny.me/api/fest/test/presence", nil)
	req.Host = "dope.pecheny.me"
	req.Header.Set("Origin", "https://dope.pecheny.kz")
	req.Header.Set("X-Forwarded-Host", "dope.pecheny.kz")
	resp := httptest.NewRecorder()

	if dopeserver.RequireSameOriginUnsafe(resp, req) {
		t.Fatal("same-origin check accepted untrusted forwarded host")
	}
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
}

func TestRequireSameOriginUnsafeAcceptsTrustedOriginHost(t *testing.T) {
	t.Setenv(dopeserver.TrustedOriginHostsEnv, "https://dope.pecheny.kz, dope.pecheny.test")
	req := httptest.NewRequest(http.MethodPost, "https://dope.pecheny.me/api/fest/test/presence", nil)
	req.Host = "dope.pecheny.me"
	req.Header.Set("Origin", "https://dope.pecheny.kz")
	resp := httptest.NewRecorder()

	if !dopeserver.RequireSameOriginUnsafe(resp, req) {
		t.Fatalf("same-origin check rejected configured origin host: status %d body %q", resp.Code, resp.Body.String())
	}
}

func TestRequireSameOriginUnsafeRejectsMismatchedForwardedHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://dope.pecheny.me/api/fest/test/presence", nil)
	req.Host = "dope.pecheny.me"
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("X-Forwarded-Host", "dope.pecheny.kz")
	resp := httptest.NewRecorder()

	if dopeserver.RequireSameOriginUnsafe(resp, req) {
		t.Fatal("same-origin check accepted mismatched forwarded host")
	}
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
}

func TestPasswordHashIsBcryptAndVerifies(t *testing.T) {
	hash, err := dopeserver.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("hash %q is not bcrypt", hash)
	}
	ok, upgraded, err := dopeserver.VerifyPassword(hash, "", "hunter2")
	if err != nil || !ok {
		t.Fatalf("verify ok = %v err = %v", ok, err)
	}
	if upgraded != "" {
		t.Fatalf("bcrypt hash should not request upgrade, got %q", upgraded)
	}
	ok, _, _ = dopeserver.VerifyPassword(hash, "", "wrong")
	if ok {
		t.Fatalf("verify with wrong password returned ok")
	}
}

func TestLegacySHA256PasswordVerifiesAndUpgradesToBcrypt(t *testing.T) {
	salt := "legacy-salt"
	legacy := dopeserver.LegacySHA256Password("hunter2", salt)
	if len(legacy) != 64 {
		t.Fatalf("expected hex sha256, got %q", legacy)
	}
	ok, upgraded, err := dopeserver.VerifyPassword(legacy, salt, "hunter2")
	if err != nil || !ok {
		t.Fatalf("legacy verify ok = %v err = %v", ok, err)
	}
	if !strings.HasPrefix(upgraded, "$2") {
		t.Fatalf("expected bcrypt upgrade, got %q", upgraded)
	}
	ok, _, _ = dopeserver.VerifyPassword(legacy, salt, "wrong")
	if ok {
		t.Fatalf("legacy verify accepted wrong password")
	}
}
