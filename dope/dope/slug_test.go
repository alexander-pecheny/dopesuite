package dopeserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateSlug(t *testing.T) {
	ok := []string{"a", "abc", "a-b", "fest-2026", "team-1", "x9", "9x", "123-abc", "a1b2"}
	for _, s := range ok {
		if err := validateSlug(s); err != nil {
			t.Errorf("validateSlug(%q) = %v, want nil", s, err)
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
		if err := validateSlug(s); err == nil {
			t.Errorf("validateSlug(%q) = nil, want error", s)
		}
	}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if err := validateSlug(string(long)); err == nil {
		t.Errorf("validateSlug too long should fail")
	}
}

func TestResolveFestAndGameSlug(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)

	if _, err := srv.db.Exec(`update fests set slug = 'my-fest' where id = ?`, festID); err != nil {
		t.Fatalf("set fest slug: %v", err)
	}
	if _, err := srv.db.Exec(`update games set slug = 'my-game' where id = ?`, gameID); err != nil {
		t.Fatalf("set game slug: %v", err)
	}

	gotFestBySlug, err := resolveFestID(t.Context(), srv.db, "my-fest")
	if err != nil || gotFestBySlug != festID {
		t.Fatalf("resolveFestID(slug) = %d, %v; want %d", gotFestBySlug, err, festID)
	}
	gotFestByID, err := resolveFestID(t.Context(), srv.db, fmt.Sprintf("%d", festID))
	if err != nil || gotFestByID != festID {
		t.Fatalf("resolveFestID(id) = %d, %v; want %d", gotFestByID, err, festID)
	}

	gotGameBySlug, err := resolveGameID(t.Context(), srv.db, festID, "my-game")
	if err != nil || gotGameBySlug != gameID {
		t.Fatalf("resolveGameID(slug) = %d, %v; want %d", gotGameBySlug, err, gameID)
	}
	gotGameByID, err := resolveGameID(t.Context(), srv.db, festID, fmt.Sprintf("%d", gameID))
	if err != nil || gotGameByID != gameID {
		t.Fatalf("resolveGameID(id) = %d, %v; want %d", gotGameByID, err, gameID)
	}
}

func TestScopedAPIAcceptsSlugRefs(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	if _, err := srv.db.Exec(`update fests set slug = 'my-fest' where id = ?`, festID); err != nil {
		t.Fatalf("set fest slug: %v", err)
	}
	if _, err := srv.db.Exec(`update games set slug = 'my-game' where id = ?`, gameID); err != nil {
		t.Fatalf("set game slug: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/fest/my-fest/games/my-game", nil)
	resp := httptest.NewRecorder()
	srv.handleScopedAPI(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("slug route status = %d, body %s", resp.Code, resp.Body.String())
	}
}

func TestPublicFestRouterAcceptsSlug(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	if _, err := srv.db.Exec(`update fests set slug = 'my-fest', is_public = 1 where id = ?`, festID); err != nil {
		t.Fatalf("set fest slug: %v", err)
	}
	srv.assets = staticFiles

	req := httptest.NewRequest(http.MethodGet, "/fest/my-fest", nil)
	resp := httptest.NewRecorder()
	srv.handleFestRouter(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("public slug route status = %d", resp.Code)
	}
}
