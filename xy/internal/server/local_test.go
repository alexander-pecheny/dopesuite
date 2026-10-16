package server

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// newLocalTestServer opens a local-mode server on a temp directory, the way
// localserver does, behind an httptest server.
func newLocalTestServer(t *testing.T) (*apiClient, *Server) {
	t.Helper()
	dir := t.TempDir()
	srv, err := Open(Config{DB: filepath.Join(dir, "xy.db"), Blobs: filepath.Join(dir, "blobs"), Local: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &apiClient{t: t, base: ts.URL}, srv
}

func plainBoard(name string) map[string]string {
	return map[string]string{"name": name, "kdf_params": plainKDFParams, "kdf_salt": "", "wrapped_key": "", "verify_token": ""}
}

func createdID(t *testing.T, c *apiClient, resp *http.Response) int64 {
	t.Helper()
	mustStatus(t, resp, http.StatusOK)
	var out struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &out)
	return out.ID
}

func TestLocalMeWithoutCookie(t *testing.T) {
	c, _ := newLocalTestServer(t)
	resp := c.do("GET", "/api/auth/me", nil)
	mustStatus(t, resp, http.StatusOK)
	var me meResponse
	c.decode(resp, &me)
	if me.UserID == 0 || me.Username == nil || *me.Username == "" {
		t.Fatalf("me = %+v, want the local user", me)
	}
	if me.Instance == nil || me.Instance.Mode != "local" || len(me.Instance.Off) == 0 {
		t.Fatalf("instance = %+v, want local with features off", me.Instance)
	}
	if len(c.jar) != 0 {
		t.Fatalf("local server set cookies: %v", c.jar)
	}
}

// TestLocalPlaintextRoundTrip writes a board's content through the API and reads
// it back both through the API and straight out of SQLite, where it must be the
// plaintext itself.
func TestLocalPlaintextRoundTrip(t *testing.T) {
	c, srv := newLocalTestServer(t)
	const question = "Вопрос 1. Кто написал «Войну и мир»?\nОтвет: Толстой."
	bid := createdID(t, c, c.do("POST", "/api/boards", plainBoard("Пакет")))
	lid := createdID(t, c, c.do("POST", "/api/boards/"+itoa(bid)+"/lists", map[string]string{"title": enc("Тур 1"), "rank": "m"}))
	cid := createdID(t, c, c.do("POST", "/api/lists/"+itoa(lid)+"/cards", map[string]string{"description": enc(question), "rank": "m"}))
	labelID := createdID(t, c, c.do("POST", "/api/boards/"+itoa(bid)+"/labels", map[string]string{"name": enc("взяли"), "color": enc("#88cc88")}))
	mustStatus(t, c.do("PUT", "/api/cards/"+itoa(cid)+"/labels", map[string]any{"labels": []map[string]any{{"label_id": labelID}}}), http.StatusNoContent)
	mustStatus(t, c.do("POST", "/api/cards/"+itoa(cid)+"/comments", map[string]string{"payload": enc(`{"text":"хороший вопрос"}`)}), http.StatusNoContent)
	attID := uploadPlain(t, c, cid, []byte("PNG-BYTES"))

	resp := c.do("GET", "/api/boards/"+itoa(bid), nil)
	mustStatus(t, resp, http.StatusOK)
	var snap boardSnapshot
	c.decode(resp, &snap)
	if len(snap.Cards) != 1 || dec(snap.Cards[0].Desc) != question || dec(snap.Lists[0].Title) != "Тур 1" ||
		dec(snap.Labels[0].Name) != "взяли" || len(snap.CardLabels) != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	resp = c.do("GET", "/api/attachments/"+itoa(attID), nil)
	mustStatus(t, resp, http.StatusOK)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "PNG-BYTES" {
		t.Fatalf("attachment = %q", got)
	}

	db := srv.s.db
	ctx := context.Background()
	for _, q := range []struct {
		sql  string
		id   int64
		want string
	}{
		{`select cast(description as text) from cards where id = ?`, cid, question},
		{`select cast(title as text) from lists where id = ?`, lid, "Тур 1"},
		{`select cast(name as text) from labels where id = ?`, labelID, "взяли"},
		{`select cast(payload as text) from timeline_events where card_id = ? and type = 'comment'`, cid, `{"text":"хороший вопрос"}`},
		{`select cast(filename as text) from attachments where id = ?`, attID, "photo.png"},
	} {
		var have string
		if err := db.QueryRowContext(ctx, q.sql, q.id).Scan(&have); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
		if have != q.want {
			t.Errorf("%s = %q, want %q", q.sql, have, q.want)
		}
	}
}

func uploadPlain(t *testing.T, c *apiClient, cardID int64, data []byte) int64 {
	t.Helper()
	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("meta", `{"filename":"`+enc("photo.png")+`","mime":"image/png","lossless":true,"event_payload":"`+enc(`{"file":"photo.png"}`)+`"}`)
	fw, _ := mw.CreateFormFile("blob", "blob")
	fw.Write(data)
	mw.Close()
	resp, err := http.Post(c.base+"/api/cards/"+itoa(cardID)+"/attachments", mw.FormDataContentType(), body)
	if err != nil {
		t.Fatal(err)
	}
	return createdID(t, c, resp)
}

// TestLocalKeymeta: a local server makes only plaintext boards and a server only
// encrypted ones, so neither kind can turn up where it does not belong.
func TestLocalKeymeta(t *testing.T) {
	encrypted := map[string]string{"name": "b", "kdf_salt": enc("s"), "kdf_params": `{"kdf":"scrypt","N":65536,"r":8,"p":1}`, "wrapped_key": enc("w"), "verify_token": enc("v")}
	half := map[string]string{"name": "b", "kdf_params": plainKDFParams, "kdf_salt": enc("s"), "wrapped_key": "", "verify_token": ""}

	local, _ := newLocalTestServer(t)
	mustStatus(t, local.do("POST", "/api/boards", encrypted), http.StatusBadRequest)
	mustStatus(t, local.do("POST", "/api/boards", half), http.StatusBadRequest)
	bid := createdID(t, local, local.do("POST", "/api/boards", plainBoard("b")))
	resp := local.do("GET", "/api/boards/"+itoa(bid)+"/keymeta", nil)
	mustStatus(t, resp, http.StatusOK)
	var km keymetaResponse
	local.decode(resp, &km)
	if km.KDFParams != plainKDFParams || km.KDFSalt != "" || km.WrappedKey != "" || km.VerifyToken != "" {
		t.Fatalf("keymeta = %+v", km)
	}

	ts, srv := newTestServer(t)
	remote := registerUser(t, srv, ts, 557001, "keymeta-user")
	mustStatus(t, remote.do("POST", "/api/boards", plainBoard("b")), http.StatusBadRequest)
	sbid := createdID(t, remote, remote.do("POST", "/api/boards", encrypted))
	mustStatus(t, remote.do("PUT", "/api/boards/"+itoa(sbid)+"/keymeta",
		map[string]string{"kdf_params": plainKDFParams, "kdf_salt": "", "wrapped_key": ""}), http.StatusBadRequest)
}

// TestLocalTestSessions: the owner records tests alone, so a local board keeps
// its sessions and Playings.
func TestLocalTestSessions(t *testing.T) {
	c, _ := newLocalTestServer(t)
	bid := createdID(t, c, c.do("POST", "/api/boards", plainBoard("b")))
	lid := createdID(t, c, c.do("POST", "/api/boards/"+itoa(bid)+"/lists", map[string]string{"title": enc("Тур 1"), "rank": "m"}))
	cid := createdID(t, c, c.do("POST", "/api/lists/"+itoa(lid)+"/cards", map[string]string{"description": enc("Вопрос"), "rank": "m"}))
	sid := createdID(t, c, c.do("POST", "/api/boards/"+itoa(bid)+"/sessions", map[string]string{"meta": enc(`{"title":"Тест"}`)}))
	resp := c.do("PUT", "/api/cards/"+itoa(cid)+"/sessions", map[string]any{"session_ids": []int64{sid}})
	if resp.StatusCode >= http.StatusBadRequest {
		t.Fatalf("set card sessions = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = c.do("GET", "/api/boards/"+itoa(bid), nil)
	mustStatus(t, resp, http.StatusOK)
	var snap boardSnapshot
	c.decode(resp, &snap)
	if len(snap.Sessions) != 1 || len(snap.CardSessions) != 1 {
		t.Fatalf("sessions = %+v, playings = %+v", snap.Sessions, snap.CardSessions)
	}
}

func TestLocalDisabledRoutes(t *testing.T) {
	c, _ := newLocalTestServer(t)
	bid := createdID(t, c, c.do("POST", "/api/boards", plainBoard("b")))
	b := itoa(bid)
	for _, r := range []struct{ method, path string }{
		{"GET", "/login"},
		{"GET", "/register"},
		{"GET", "/profile/tokens"},
		{"GET", "/join/abc"},
		{"GET", "/admin"},
		{"POST", "/api/auth/tg/start"},
		{"POST", "/api/auth/login-password"},
		{"POST", "/api/auth/logout"},
		{"POST", "/api/auth/password"},
		{"GET", "/api/tokens"},
		{"POST", "/api/tokens"},
		{"GET", "/1/boards/" + b},
		{"GET", "/api/boards/" + b + "/members"},
		{"POST", "/api/boards/" + b + "/members"},
		{"DELETE", "/api/boards/" + b + "/membership"},
		{"GET", "/api/collaborators"},
		{"PUT", "/api/boards/" + b + "/keymeta"},
		{"GET", "/api/boards/" + b + "/invites"},
		{"POST", "/api/boards/" + b + "/invites"},
		{"GET", "/api/board-invites/code/abc"},
	} {
		resp := c.do(r.method, r.path, map[string]string{})
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", r.method, r.path, resp.StatusCode)
		}
	}
	for _, path := range []string{"/", "/board/" + b, "/profile", "/import"} {
		resp := c.do("GET", path, nil)
		mustStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
}

func TestLocalUserSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DB: filepath.Join(dir, "xy.db"), Blobs: filepath.Join(dir, "blobs"), Local: true}
	first, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := first.s.localUser.UserID
	first.Close()
	second, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.s.localUser.UserID != id {
		t.Fatalf("reopened as user %d, want %d", second.s.localUser.UserID, id)
	}
}
