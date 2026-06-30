package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"xy/internal/blobstore"
)

// newTestServer spins up a server backed by a temp SQLite DB and an httptest
// server with the full route set.
func newTestServer(t *testing.T) (*httptest.Server, *server) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, err := blobstore.New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("blobstore: %v", err)
	}
	srv := &server{db: db, blobs: blobs, staging: newHandoutStaging()}
	srv.assetSource, _ = staticSource()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/register/start", srv.handleRegisterStart)
	mux.HandleFunc("GET /api/auth/register/status", srv.handleRegisterStatus)
	mux.HandleFunc("POST /api/auth/login/start", srv.handleLoginStart)
	mux.HandleFunc("POST /api/auth/login", srv.handleLoginCode)
	mux.HandleFunc("POST /api/auth/login-password", srv.handleLoginPassword)
	mux.HandleFunc("POST /api/auth/logout", srv.handleLogout)
	mux.HandleFunc("GET /api/auth/me", srv.handleMe)
	mux.HandleFunc("POST /api/auth/username", srv.handleSetUsername)
	mux.HandleFunc("POST /api/auth/password", srv.handleSetPassword)
	mux.HandleFunc("GET /api/boards", srv.handleListBoards)
	mux.HandleFunc("POST /api/boards", srv.handleCreateBoard)
	mux.HandleFunc("GET /api/boards/{id}", srv.handleGetBoard)
	mux.HandleFunc("GET /api/boards/{id}/keymeta", srv.handleGetKeymeta)
	mux.HandleFunc("POST /api/boards/{id}/lists", srv.handleCreateList)
	mux.HandleFunc("PATCH /api/lists/{id}", srv.handlePatchList)
	mux.HandleFunc("DELETE /api/lists/{id}", srv.handleDeleteList)
	mux.HandleFunc("POST /api/boards/{id}/list-groups", srv.handleCreateListGroup)
	mux.HandleFunc("PATCH /api/list-groups/{id}", srv.handlePatchListGroup)
	mux.HandleFunc("DELETE /api/list-groups/{id}", srv.handleDeleteListGroup)
	mux.HandleFunc("POST /api/lists/{id}/cards", srv.handleCreateCard)
	mux.HandleFunc("PATCH /api/cards/{id}", srv.handlePatchCard)
	mux.HandleFunc("POST /api/boards/{id}/labels", srv.handleCreateLabel)
	mux.HandleFunc("PUT /api/cards/{id}/labels", srv.handleSetCardLabels)
	mux.HandleFunc("GET /api/cards/{id}/timeline", srv.handleGetTimeline)
	mux.HandleFunc("POST /api/cards/{id}/comments", srv.handleAddComment)
	mux.HandleFunc("POST /api/cards/{id}/comments/import", srv.handleImportComments)
	mux.HandleFunc("GET /api/cards/{id}/attachments", srv.handleListAttachments)
	mux.HandleFunc("POST /api/cards/{id}/attachments", srv.handleCreateAttachment)
	mux.HandleFunc("GET /api/attachments/{id}", srv.handleGetAttachment)
	mux.HandleFunc("DELETE /api/attachments/{id}", srv.handleDeleteAttachment)
	mux.HandleFunc("POST /api/export/docx", srv.handleExportDocx)
	mux.HandleFunc("POST /api/handouts/pdf", srv.handleHandoutsPDF)
	mux.HandleFunc("POST /api/handouts/stage", srv.handleHandoutsStage)
	mux.HandleFunc("POST /api/handouts/heartbeat", srv.handleHandoutsHeartbeat)
	mux.HandleFunc("DELETE /api/handouts/stage", srv.handleHandoutsUnstage)
	mux.HandleFunc("GET /api/tokens", srv.handleListTokens)
	mux.HandleFunc("POST /api/tokens", srv.handleCreateToken)
	mux.HandleFunc("DELETE /api/tokens/{id}", srv.handleRevokeToken)
	mux.HandleFunc("GET /admin", srv.HandleAdminLanding)
	mux.HandleFunc("GET /admin/create_users", srv.HandleAdminCreateUsers)
	mux.HandleFunc("POST /admin/create_users", srv.HandleAdminCreateUsers)
	mux.HandleFunc("GET /1/boards/{id}", srv.handleTrelloGetBoard)
	mux.HandleFunc("GET /1/boards/{id}/lists", srv.handleTrelloGetLists)
	mux.HandleFunc("POST /1/lists/{id}/cards", srv.handleTrelloCreateCard)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, srv
}

type apiClient struct {
	t    *testing.T
	base string
	jar  []*http.Cookie
}

func (c *apiClient) do(method, path string, body any) *http.Response {
	c.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	if cks := resp.Cookies(); len(cks) > 0 {
		c.jar = cks
	}
	return resp
}

func (c *apiClient) decode(resp *http.Response, v any) {
	c.t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		c.t.Fatalf("decode: %v", err)
	}
}

func mustStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		resp.Body.Close()
		t.Fatalf("status = %d, want %d; body: %s", resp.StatusCode, want, buf.String())
	}
}

// TestFullFlow exercises register (simulating the telegram bot) → session →
// board create → list → card → patch → label → comment → snapshot.
func TestFullFlow(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()

	// Mint an invite directly (as the `invite` subcommand would).
	invite, err := srv.mintInvite(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	c := &apiClient{t: t, base: ts.URL}

	// register/start with the invite -> register code
	resp := c.do("POST", "/api/auth/register/start", map[string]string{"invite_code": invite})
	mustStatus(t, resp, 200)
	var rs registerStartResponse
	c.decode(resp, &rs)
	if rs.Code == "" {
		t.Fatal("no register code")
	}

	// Simulate the telegram bot consuming the register code.
	if err := srv.simulateBotRegister(ctx, rs.Code, 555001, "tester"); err != nil {
		t.Fatalf("bot register: %v", err)
	}

	// register/status -> ready + session cookie
	resp = c.do("GET", "/api/auth/register/status?code="+rs.Code, nil)
	mustStatus(t, resp, 200)
	var st registerStatusResponse
	c.decode(resp, &st)
	if st.Status != "ready" {
		t.Fatalf("status = %q, want ready", st.Status)
	}

	// /api/auth/me should now resolve
	resp = c.do("GET", "/api/auth/me", nil)
	mustStatus(t, resp, 200)
	var me meResponse
	c.decode(resp, &me)
	if me.UserID == 0 {
		t.Fatal("no user id")
	}

	// set a username, then a password, then login by password in a fresh client
	resp = c.do("POST", "/api/auth/username", map[string]string{"username": "tester"})
	mustStatus(t, resp, 204)
	resp = c.do("POST", "/api/auth/password", map[string]string{"new_password": "hunter2hunter"})
	mustStatus(t, resp, 204)

	c2 := &apiClient{t: t, base: ts.URL}
	resp = c2.do("POST", "/api/auth/login-password", map[string]string{"username": "tester", "password": "hunter2hunter"})
	mustStatus(t, resp, 200)

	// create a board (fake ciphertext fields — server treats them as opaque)
	board := map[string]string{
		"name_enc":     enc("my board"),
		"kdf_salt":     enc("salt"),
		"kdf_params":   `{"kdf":"scrypt","N":32768,"r":8,"p":1}`,
		"wrapped_key":  enc("wrapped"),
		"verify_token": enc("verify"),
	}
	resp = c.do("POST", "/api/boards", board)
	mustStatus(t, resp, 200)
	var created struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &created)
	boardID := itoa(created.ID)

	// keymeta round-trips
	resp = c.do("GET", "/api/boards/"+boardID+"/keymeta", nil)
	mustStatus(t, resp, 200)
	var km keymetaResponse
	c.decode(resp, &km)
	if km.KDFParams == "" {
		t.Fatal("no kdf params")
	}

	// create a list
	resp = c.do("POST", "/api/boards/"+boardID+"/lists", map[string]string{"title_enc": enc("To do"), "rank": "m"})
	mustStatus(t, resp, 200)
	var listC struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &listC)
	listID := itoa(listC.ID)

	// create a card
	resp = c.do("POST", "/api/lists/"+listID+"/cards", map[string]string{"description_enc": enc("first question"), "rank": "m"})
	mustStatus(t, resp, 200)
	var cardC struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &cardC)
	cardID := itoa(cardC.ID)

	// patch the card description + append a desc_edit event
	resp = c.do("PATCH", "/api/cards/"+cardID, map[string]string{
		"description_enc": enc("edited question"),
		"desc_event_enc":  enc(`{"before":"first question","after":"edited question"}`),
	})
	mustStatus(t, resp, 204)

	// create a label and assign it
	resp = c.do("POST", "/api/boards/"+boardID+"/labels", map[string]string{"name_enc": enc("hard"), "color_enc": enc("#ff0000")})
	mustStatus(t, resp, 200)
	var labelC struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &labelC)
	resp = c.do("PUT", "/api/cards/"+cardID+"/labels", map[string]any{
		"label_ids": []int64{labelC.ID},
		"events":    []map[string]string{{"type": "label_add", "payload_enc": enc(`{"label":"hard"}`)}},
	})
	mustStatus(t, resp, 204)

	// add a comment
	resp = c.do("POST", "/api/cards/"+cardID+"/comments", map[string]string{"payload_enc": enc("looks good")})
	mustStatus(t, resp, 204)

	// timeline should have 3 events: desc_edit, label_add, comment
	resp = c.do("GET", "/api/cards/"+cardID+"/timeline", nil)
	mustStatus(t, resp, 200)
	var tl []timelineEventDTO
	c.decode(resp, &tl)
	if len(tl) != 3 {
		t.Fatalf("timeline len = %d, want 3", len(tl))
	}

	// snapshot reflects everything
	resp = c.do("GET", "/api/boards/"+boardID, nil)
	mustStatus(t, resp, 200)
	var snap boardSnapshot
	c.decode(resp, &snap)
	if len(snap.Lists) != 1 || len(snap.Cards) != 1 || len(snap.Labels) != 1 {
		t.Fatalf("snapshot = %d lists, %d cards, %d labels", len(snap.Lists), len(snap.Cards), len(snap.Labels))
	}
	if got := snap.CardLabels[itoa(cardC.ID)]; len(got) != 1 || got[0] != labelC.ID {
		t.Fatalf("card_labels = %v, want [%d]", got, labelC.ID)
	}
	if dec(snap.Cards[0].DescEnc) != "edited question" {
		t.Fatalf("desc = %q", dec(snap.Cards[0].DescEnc))
	}

	// an outsider (no membership) cannot read the board
	c3 := registerUser(t, srv, ts, 555002, "outsider")
	resp = c3.do("GET", "/api/boards/"+boardID, nil)
	mustStatus(t, resp, 403)
}

func TestUnauthRejected(t *testing.T) {
	ts, _ := newTestServer(t)
	c := &apiClient{t: t, base: ts.URL}
	resp := c.do("GET", "/api/boards", nil)
	mustStatus(t, resp, 401)
}
