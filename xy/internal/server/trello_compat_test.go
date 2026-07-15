package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"pecheny.me/dopecore/authcred"
)

func TestTrelloCompatAPI(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770001, "author")

	// board
	resp := c.do("POST", "/api/boards", map[string]string{
		"name": "Пакет", "kdf_salt": enc("salt"),
		"kdf_params": `{"kdf":"scrypt"}`, "wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	mustStatus(t, resp, 200)
	var board struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &board)
	boardID := itoa(board.ID)

	// list
	resp = c.do("POST", "/api/boards/"+boardID+"/lists", map[string]string{"title_enc": enc("Тур 1"), "rank": "a1"})
	mustStatus(t, resp, 200)
	var list struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &list)
	listID := itoa(list.ID)

	// card
	resp = c.do("POST", "/api/lists/"+listID+"/cards", map[string]string{"description_enc": enc("first question"), "rank": "a1"})
	mustStatus(t, resp, 200)
	var card struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &card)
	cardID := itoa(card.ID)

	// label + assign
	resp = c.do("POST", "/api/boards/"+boardID+"/labels", map[string]string{"name_enc": enc("сложный"), "color_enc": enc("#f00")})
	mustStatus(t, resp, 200)
	var label struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &label)
	resp = c.do("PUT", "/api/cards/"+cardID+"/labels", map[string]any{"label_ids": []int64{label.ID}})
	mustStatus(t, resp, 204)

	// mint an API token (session-authed)
	resp = c.do("POST", "/api/tokens", map[string]string{"label": "chgksuite"})
	mustStatus(t, resp, 200)
	var tok createTokenResponse
	c.decode(resp, &tok)
	if tok.Token == "" {
		t.Fatal("no raw token returned")
	}

	tokenAuthed := func(method, path string, form url.Values) *http.Response {
		t.Helper()
		var req *http.Request
		var err error
		if method == "POST" {
			form.Set("token", tok.Token)
			req, err = http.NewRequest("POST", ts.URL+path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			q := url.Values{"token": {tok.Token}, "key": {"ignored"}}
			req, err = http.NewRequest(method, ts.URL+path+"?"+q.Encode(), nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// ---- GET /1/boards/{id} ----
	resp = tokenAuthed("GET", "/1/boards/"+boardID, nil)
	mustStatus(t, resp, 200)
	var tb trelloBoard
	c.decode(resp, &tb)
	if tb.ID != boardID {
		t.Fatalf("board id = %q, want %q", tb.ID, boardID)
	}
	// Board name is plaintext now (schema_version 2), not a ciphertext envelope.
	if tb.Name != "Пакет" {
		t.Fatalf("board name = %q, want plaintext %q", tb.Name, "Пакет")
	}
	// keymeta must travel with the board so a token holder can derive the DK.
	if tb.Keymeta.KDFParams != `{"kdf":"scrypt"}` || dec(tb.Keymeta.KDFSalt) != "salt" ||
		dec(tb.Keymeta.WrappedKey) != "w" || dec(tb.Keymeta.VerifyToken) != "v" {
		t.Fatalf("keymeta = %+v", tb.Keymeta)
	}
	if len(tb.Lists) != 1 || tb.Lists[0].ID != listID || dec(tb.Lists[0].Name) != "Тур 1" {
		t.Fatalf("lists = %+v", tb.Lists)
	}
	if len(tb.Cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(tb.Cards))
	}
	tc := tb.Cards[0]
	if tc.ID != cardID || tc.IDList != listID {
		t.Fatalf("card ids: id=%q idList=%q", tc.ID, tc.IDList)
	}
	if dec(tc.Desc) != "first question" {
		t.Fatalf("card desc decodes to %q", dec(tc.Desc))
	}
	if len(tc.Labels) != 1 || dec(tc.Labels[0].Name) != "сложный" {
		t.Fatalf("card labels = %+v", tc.Labels)
	}

	// ---- GET /1/boards/{id}/lists ----
	resp = tokenAuthed("GET", "/1/boards/"+boardID+"/lists", nil)
	mustStatus(t, resp, 200)
	var lists []trelloList
	c.decode(resp, &lists)
	if len(lists) != 1 || dec(lists[0].Name) != "Тур 1" {
		t.Fatalf("lists endpoint = %+v", lists)
	}

	// ---- POST /1/lists/{id}/cards (upload) ----
	resp = tokenAuthed("POST", "/1/lists/"+listID+"/cards", url.Values{
		"name": {"вопрос"}, "desc": {enc("uploaded question")},
	})
	mustStatus(t, resp, 200)
	var newCard trelloCard
	c.decode(resp, &newCard)
	if dec(newCard.Desc) != "uploaded question" {
		t.Fatalf("created card desc = %q", dec(newCard.Desc))
	}

	// the new card is now visible (snapshot, decryptable) and ordered after the first
	resp = c.do("GET", "/api/boards/"+boardID, nil)
	mustStatus(t, resp, 200)
	var snap boardSnapshot
	c.decode(resp, &snap)
	if len(snap.Cards) != 2 {
		t.Fatalf("snapshot cards = %d, want 2", len(snap.Cards))
	}
	if dec(snap.Cards[1].DescEnc) != "uploaded question" {
		t.Fatalf("uploaded card not last: %q", dec(snap.Cards[1].DescEnc))
	}
	if !(snap.Cards[1].Rank > snap.Cards[0].Rank) {
		t.Fatalf("rank not increasing: %q !> %q", snap.Cards[1].Rank, snap.Cards[0].Rank)
	}

	// plaintext (non-base64) desc is rejected
	resp = tokenAuthed("POST", "/1/lists/"+listID+"/cards", url.Values{"desc": {"!! not base64 !!"}})
	mustStatus(t, resp, 400)
}

func TestTrelloAuthFailures(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770010, "owner")
	resp := c.do("POST", "/api/boards", map[string]string{
		"name": "B", "kdf_salt": enc("s"),
		"kdf_params": `{}`, "wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	mustStatus(t, resp, 200)
	var board struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &board)
	boardID := itoa(board.ID)

	resp = c.do("POST", "/api/tokens", map[string]string{"label": "t"})
	mustStatus(t, resp, 200)
	var tok createTokenResponse
	c.decode(resp, &tok)

	get := func(q string) *http.Response {
		r, err := http.Get(ts.URL + "/1/boards/" + boardID + q)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// missing + bad token -> 401
	mustStatus(t, get(""), 401)
	mustStatus(t, get("?token=deadbeef"), 401)
	// valid token -> 200
	mustStatus(t, get("?token="+tok.Token), 200)

	// an outsider's valid token cannot reach this board -> 403
	outsider := registerUser(t, srv, ts, 770011, "outsider")
	resp = outsider.do("POST", "/api/tokens", map[string]string{})
	mustStatus(t, resp, 200)
	var otok createTokenResponse
	outsider.decode(resp, &otok)
	mustStatus(t, get("?token="+otok.Token), 403)

	// expire the token in-place -> 401
	if _, err := srv.db.ExecContext(context.Background(),
		`update api_tokens set expires_at = ? where token_hash = ?`,
		rfc3339(time.Now().Add(-time.Hour)), authcred.HashSessionToken(tok.Token)); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, get("?token="+tok.Token), 401)

	// revoke a fresh token -> 401
	resp = c.do("POST", "/api/tokens", map[string]string{"label": "t2"})
	mustStatus(t, resp, 200)
	var tok2 createTokenResponse
	c.decode(resp, &tok2)
	var tok2ID int64
	if err := srv.db.QueryRowContext(context.Background(),
		`select id from api_tokens where token_hash = ?`, authcred.HashSessionToken(tok2.Token)).Scan(&tok2ID); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, c.do("DELETE", "/api/tokens/"+itoa(tok2ID), nil), 204)
	mustStatus(t, get("?token="+tok2.Token), 401)
}
