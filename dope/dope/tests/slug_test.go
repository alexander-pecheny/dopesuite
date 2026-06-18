package tests

import (
	dopeserver "dope/dope"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"dope/dope/store"
	"dope/dope/util"
)

func TestValidateSlug(t *testing.T) {
	ok := []string{"a", "abc", "a-b", "fest-2026", "team-1", "x9", "9x", "123-abc", "a1b2"}
	for _, s := range ok {
		if err := util.ValidateSlug(s); err != nil {
			t.Errorf("util.ValidateSlug(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{
		"",
		"123",
		"0",
		"Abc",
		"a b",
		"a_b",
		"a/b",
		"абв",
	}
	for _, s := range bad {
		if err := util.ValidateSlug(s); err == nil {
			t.Errorf("util.ValidateSlug(%q) = nil, want error", s)
		}
	}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if err := util.ValidateSlug(string(long)); err == nil {
		t.Errorf("util.ValidateSlug too long should fail")
	}
}

func TestResolveFestAndGameSlug(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)

	if _, err := srv.Eng().DB.Exec(`update fests set slug = 'my-fest' where id = ?`, festID); err != nil {
		t.Fatalf("set fest slug: %v", err)
	}
	if _, err := srv.Eng().DB.Exec(`update games set slug = 'my-game' where id = ?`, gameID); err != nil {
		t.Fatalf("set game slug: %v", err)
	}

	gotFestBySlug, err := store.ResolveFestID(t.Context(), srv.Eng().DB, "my-fest")
	if err != nil || gotFestBySlug != festID {
		t.Fatalf("store.ResolveFestID(slug) = %d, %v; want %d", gotFestBySlug, err, festID)
	}
	gotFestByID, err := store.ResolveFestID(t.Context(), srv.Eng().DB, fmt.Sprintf("%d", festID))
	if err != nil || gotFestByID != festID {
		t.Fatalf("store.ResolveFestID(id) = %d, %v; want %d", gotFestByID, err, festID)
	}

	gotGameBySlug, err := dopeserver.ResolveGameID(t.Context(), srv.Eng().DB, festID, "my-game")
	if err != nil || gotGameBySlug != gameID {
		t.Fatalf("dopeserver.ResolveGameID(slug) = %d, %v; want %d", gotGameBySlug, err, gameID)
	}
	gotGameByID, err := dopeserver.ResolveGameID(t.Context(), srv.Eng().DB, festID, fmt.Sprintf("%d", gameID))
	if err != nil || gotGameByID != gameID {
		t.Fatalf("dopeserver.ResolveGameID(id) = %d, %v; want %d", gotGameByID, err, gameID)
	}
}

func TestScopedAPIAcceptsSlugRefs(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	if _, err := srv.Eng().DB.Exec(`update fests set slug = 'my-fest' where id = ?`, festID); err != nil {
		t.Fatalf("set fest slug: %v", err)
	}
	if _, err := srv.Eng().DB.Exec(`update games set slug = 'my-game' where id = ?`, gameID); err != nil {
		t.Fatalf("set game slug: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/fest/my-fest/games/my-game", nil)
	resp := httptest.NewRecorder()
	srv.HandleScopedAPI(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("slug route status = %d, body %s", resp.Code, resp.Body.String())
	}
}

func TestPublicFestRouterAcceptsSlug(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	if _, err := srv.Eng().DB.Exec(`update fests set slug = 'my-fest', is_public = 1 where id = ?`, festID); err != nil {
		t.Fatalf("set fest slug: %v", err)
	}
	srv.Eng().Assets = dopeserver.StaticFiles

	req := httptest.NewRequest(http.MethodGet, "/fest/my-fest", nil)
	resp := httptest.NewRecorder()
	srv.HandleFestRouter(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("public slug route status = %d", resp.Code)
	}
}
