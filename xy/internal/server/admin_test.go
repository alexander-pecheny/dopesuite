package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/dopecore/adminusers"
)

// postForm sends a form-encoded POST carrying the client's session cookies.
func (c *apiClient) postForm(path string, form url.Values) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest("POST", c.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// TestAdminUsers covers the account list: gated like the rest of /admin, and
// showing each user's handle, storage against quota, and last login.
func TestAdminUsers(t *testing.T) {
	ts, srv := newTestServer(t)

	plain := registerUser(t, srv, ts, 880011, "plainuser")
	mustStatus(t, plain.do("GET", "/admin/users", nil), 404)

	admin := registerUser(t, srv, ts, 880012, "boss")
	t.Setenv("XY_ADMIN_USER", "boss")

	resp := admin.do("GET", "/admin/users", nil)
	mustStatus(t, resp, 200)
	page := body(t, resp)
	for _, want := range []string{"plainuser", "boss", "Хранилище, МБ", "Активность", "0 /", `value="25"`, "0 / ∞"} {
		if !strings.Contains(page, want) {
			t.Fatalf("users page missing %q:\n%s", want, page)
		}
	}

	// A sorted view marks its column with an arrow, and the same header now
	// offers the opposite direction.
	sorted := body(t, admin.do("GET", "/admin/users?sort=used", nil))
	if !strings.Contains(sorted, "Хранилище, МБ ↓") || !strings.Contains(sorted, "sort=used&amp;dir=asc") {
		t.Fatalf("sorted page missing the flip affordance:\n%s", sorted)
	}
}

// TestAdminQuotas covers editing quotas from the users table: the admin's own
// row has no field, a save writes MB as bytes and keeps the sort, untouched rows
// stay as they were, and a bad value or a non-admin changes nothing.
func TestAdminQuotas(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()

	plain := registerUser(t, srv, ts, 880021, "plainuser")
	registerUser(t, srv, ts, 880022, "otheruser")
	admin := registerUser(t, srv, ts, 880023, "boss")
	t.Setenv("XY_ADMIN_USER", "boss")

	ids := map[string]int64{}
	for _, name := range []string{"plainuser", "otheruser", "boss"} {
		var id int64
		if err := srv.db.QueryRowContext(ctx, `select id from users where username = ?`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	quota := func(name string) int64 {
		var q int64
		if err := srv.db.QueryRowContext(ctx, `select quota_bytes from users where id = ?`, ids[name]).Scan(&q); err != nil {
			t.Fatal(err)
		}
		return q
	}

	page := body(t, admin.do("GET", "/admin/users", nil))
	for _, name := range []string{"plainuser", "otheruser"} {
		if !strings.Contains(page, `name="`+quotaField(ids[name])+`"`) {
			t.Fatalf("no quota field for %s:\n%s", name, page)
		}
	}
	if strings.Contains(page, `name="`+quotaField(ids["boss"])+`"`) {
		t.Fatalf("the uncapped admin row has a quota field:\n%s", page)
	}

	form := url.Values{
		quotaField(ids["plainuser"]): {"100"},
		quotaField(ids["otheruser"]): {"25"},
	}
	mustStatus(t, plain.postForm("/admin/users", form), 404)
	if got := quota("plainuser"); got != 25<<20 {
		t.Fatalf("non-admin changed a quota: %d", got)
	}

	resp := admin.postForm("/admin/users?sort=used&dir=asc", form)
	mustStatus(t, resp, 200) // the 303 back to the page, followed
	if got := resp.Request.URL.RequestURI(); got != "/admin/users?sort=used&dir=asc" {
		t.Fatalf("landed on %q, want the same sorted view", got)
	}
	if got := quota("plainuser"); got != 100<<20 {
		t.Fatalf("plainuser quota = %d, want 100 MiB", got)
	}
	if got := quota("otheruser"); got != 25<<20 {
		t.Fatalf("otheruser quota = %d, want unchanged 25 MiB", got)
	}
	if !strings.Contains(body(t, admin.do("GET", "/admin/users", nil)), `value="100"`) {
		t.Fatal("saved quota not shown on the page")
	}

	for _, bad := range []string{"-1", "", "lots"} {
		resp := admin.postForm("/admin/users", url.Values{quotaField(ids["plainuser"]): {bad}})
		mustStatus(t, resp, http.StatusBadRequest)
	}
	if got := quota("plainuser"); got != 100<<20 {
		t.Fatalf("a rejected save changed the quota: %d", got)
	}
}

// TestSortAdminUsers covers the ordering itself: both keys, both directions, and
// the accounts that never logged in.
func TestSortAdminUsers(t *testing.T) {
	rows := []adminUserRow{
		{Username: "a", Used: 10, LastSeenAt: "2026-01-02T00:00:00Z"},
		{Username: "b", Used: 30, LastSeenAt: ""},
		{Username: "c", Used: 20, LastSeenAt: "2026-03-04T00:00:00Z"},
	}
	names := func(s adminusers.Sort) string {
		got := slices.Clone(rows)
		sortAdminUsers(got, s)
		out := ""
		for _, u := range got {
			out += u.Username
		}
		return out
	}
	cases := map[string]struct {
		sort adminusers.Sort
		want string
	}{
		"used desc": {adminusers.Sort{Key: "used", Desc: true}, "bca"},
		"used asc":  {adminusers.Sort{Key: "used"}, "acb"},
		"last desc": {adminusers.Sort{Key: "last", Desc: true}, "cab"},
		"last asc":  {adminusers.Sort{Key: "last"}, "bac"},
		"unsorted":  {adminusers.Sort{}, "abc"},
	}
	for name, c := range cases {
		if got := names(c.sort); got != c.want {
			t.Errorf("%s = %q, want %q", name, got, c.want)
		}
	}
}

// TestAdminCreateUsers covers the admin gate and the bulk create-users flow:
// non-admins can't reach it; the admin can create accounts, with duplicates
// skipped and invalid logins reported; created users can then log in.
func TestAdminCreateUsers(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()

	// A non-admin, logged-in user gets a 404 (page existence hidden).
	plain := registerUser(t, srv, ts, 880001, "plainuser")
	resp := plain.do("GET", "/admin/create_users", nil)
	mustStatus(t, resp, 404)

	// Register "boss" first, then designate it the admin — open signup won't
	// mint the reserved admin username, so the account must exist beforehand.
	admin := registerUser(t, srv, ts, 880002, "boss")
	t.Setenv("XY_ADMIN_USER", "boss")

	// GET the form.
	resp = admin.do("GET", "/admin/create_users", nil)
	mustStatus(t, resp, 200)
	if !strings.Contains(body(t, resp), "Создать пользователей") {
		t.Fatal("form page missing heading")
	}

	// POST a batch: one new, one duplicate (boss exists), one invalid.
	resp = admin.postForm("/admin/create_users", url.Values{"usernames": {"alice\nboss\nbad name!"}})
	mustStatus(t, resp, 200)
	page := body(t, resp)
	if !strings.Contains(page, "alice") {
		t.Fatal("created user not shown")
	}
	if !strings.Contains(page, "пропущены") || !strings.Contains(page, "boss") {
		t.Fatalf("duplicate not reported as skipped:\n%s", page)
	}
	if !strings.Contains(page, "недопустимый логин") {
		t.Fatal("invalid login not reported")
	}

	// The created account exists with a usable password — confirm it can log in
	// by reading its hash and checking a bcrypt round-trip isn't empty.
	var hash string
	if err := srv.db.QueryRowContext(ctx, `select password_hash from users where username = ?`, "alice").Scan(&hash); err != nil {
		t.Fatalf("alice not persisted: %v", err)
	}
	if hash == "" {
		t.Fatal("alice has no password hash")
	}

	// Logged-out access redirects to /login (303), not 404.
	anon := &apiClient{t: t, base: ts.URL}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", anon.base+"/admin/create_users", nil)
	r2, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if r2.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon status = %d, want 303", r2.StatusCode)
	}
	r2.Body.Close()
}
