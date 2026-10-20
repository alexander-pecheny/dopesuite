package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/dopecore/schema"
)

// TestMigrateV30KeepsCardChildren: rebuilding lists and cards must not cascade
// away what hangs off them.
func TestMigrateV30KeepsCardChildren(t *testing.T) {
	db := migratedDBThrough(t, 29)
	uid := insertUser(t, db, "owner")
	bid := insertBoard(t, db, uid, "board")
	list := insertList(t, db, bid, "normal")
	card := insertCard(t, db, bid, list, "question", "вопрос")
	label := insertLabel(t, db, bid, "метка")
	mustExec(t, db, `insert into card_labels(card_id, label_id) values(?, ?)`, card, label)
	insertLegacyEvent(t, db, bid, card, "comment", "комментарий")
	insertAttachment(t, db, bid, card)

	if err := schema.Apply(db, migrations); err != nil {
		t.Fatalf("v30: %v", err)
	}
	for q, want := range map[string]int64{
		`select count(*) from cards`:                    1,
		`select count(*) from card_labels`:              1,
		`select count(*) from timeline_events`:          1,
		`select count(*) from attachments`:              1,
		`select count(*) from pragma_foreign_key_check`: 0,
	} {
		if got := scalarInt(t, db, q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
	mustExec(t, db, `insert into lists(board_id, type, title, rank, created_at, updated_at) values(?, 'appeals', 'a', 'b', ?, ?)`, bid, fixtureNow, fixtureNow)
	mustExec(t, db, `insert into cards(board_id, list_id, kind, description, rank, created_at, updated_at) values(?, ?, 'appeal', '{}', 'b', ?, ?)`, bid, list, fixtureNow, fixtureNow)
}

// TestAppealsListAndVotes: an appeals list keeps its settings, a member has one
// vote per appeal card, and a question takes no votes at all.
func TestAppealsListAndVotes(t *testing.T) {
	ts, srv := newTestServer(t)
	c, boardID, _ := boardWithListOn(t, ts, srv)
	resp := c.do("POST", "/api/boards/"+boardID+"/lists", map[string]any{"title": enc("Апелляции"), "rank": "n", "type": "appeals", "meta": enc(`{"tournament_id":7696}`)})
	mustStatus(t, resp, 200)
	var l struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &l)
	resp = c.do("POST", "/api/lists/"+itoa(l.ID)+"/cards", map[string]string{"description": enc("{}"), "rank": "m", "kind": "appeal"})
	mustStatus(t, resp, 200)
	var card struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &card)
	resp = c.do("POST", "/api/lists/"+itoa(l.ID)+"/cards", map[string]string{"description": enc("? q"), "rank": "n", "kind": "question"})
	mustStatus(t, resp, 200)
	var question struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &question)

	other := registerUser(t, srv, ts, 770901, "juror")
	addBoardMember(t, srv, mustAtoi(t, boardID), meUserID(t, other))

	mustStatus(t, c.do("PUT", "/api/cards/"+itoa(card.ID)+"/vote", map[string]string{"value": enc("for")}), 204)
	mustStatus(t, c.do("PUT", "/api/cards/"+itoa(card.ID)+"/vote", map[string]string{"value": enc("against")}), 204)
	mustStatus(t, other.do("PUT", "/api/cards/"+itoa(card.ID)+"/vote", map[string]string{"value": enc("for")}), 204)
	mustStatus(t, c.do("PUT", "/api/cards/"+itoa(question.ID)+"/vote", map[string]string{"value": enc("for")}), 400)

	snap := getSnap(t, c, boardID)
	got := map[int64]string{}
	for _, v := range snap.Votes {
		got[v.UserID] = dec(v.Value)
	}
	if len(got) != 2 || got[meUserID(t, c)] != "against" || got[meUserID(t, other)] != "for" {
		t.Fatalf("votes = %v", got)
	}
	for _, sl := range snap.Lists {
		if sl.ID == l.ID && (sl.Type != "appeals" || sl.Meta == nil || dec(*sl.Meta) != `{"tournament_id":7696}`) {
			t.Fatalf("appeals list = %+v", sl)
		}
	}

	mustStatus(t, other.do("DELETE", "/api/cards/"+itoa(card.ID)+"/vote", nil), 204)
	if snap = getSnap(t, c, boardID); len(snap.Votes) != 1 {
		t.Fatalf("votes after a retraction = %+v", snap.Votes)
	}

	mustStatus(t, c.do("PATCH", "/api/boards/"+boardID, map[string]string{"rating_token": enc("rt_x")}), 204)
	if snap = getSnap(t, c, boardID); snap.RatingToken == nil || dec(*snap.RatingToken) != "rt_x" || snap.Name != "b" {
		t.Fatalf("token = %v, name = %q", snap.RatingToken, snap.Name)
	}
	mustStatus(t, c.do("PATCH", "/api/boards/"+boardID, map[string]string{"rating_token": ""}), 204)
	if snap = getSnap(t, c, boardID); snap.RatingToken != nil {
		t.Fatalf("token after clearing = %v", *snap.RatingToken)
	}
}

// boardWithListOn is boardWithList on a server the test already holds, for a
// test that needs a second user on it.
func boardWithListOn(t *testing.T, ts *httptest.Server, srv *server) (*apiClient, string, string) {
	t.Helper()
	c := registerUser(t, srv, ts, 770900, "chair")
	resp := c.do("POST", "/api/boards", map[string]string{
		"name": "b", "kdf_salt": enc("s"),
		"kdf_params": `{"kdf":"scrypt","N":1,"r":1,"p":1}`, "wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	mustStatus(t, resp, 200)
	var b struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &b)
	resp = c.do("POST", "/api/boards/"+itoa(b.ID)+"/lists", map[string]string{"title": enc("L"), "rank": "m"})
	mustStatus(t, resp, 200)
	var l struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &l)
	return c, itoa(b.ID), itoa(l.ID)
}

// TestRatingRelay: the relay forwards the token, hands back both lists, keeps
// the appeals when the controversials are refused, and passes the site's own
// words through on a refused verdict.
func TestRatingRelay(t *testing.T) {
	var seen []string
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+string(body))
		switch r.URL.Path {
		case "/tournaments/7/appeals.json":
			_, _ = w.Write([]byte(`[{"id":1,"questionNumber":3}]`))
		case "/tournaments/7/controversials.json":
			_, _ = w.Write([]byte(`[{"id":9,"questionNumber":3}]`))
		case "/tournaments/8/appeals.json":
			_, _ = w.Write([]byte(`[]`))
		case "/tournament_synch_appeals/1/verdict":
			_, _ = w.Write([]byte(`{"id":1,"status":"A"}`))
		case "/tournament_synch_appeals/2/verdict":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"violations":[{"message":"подача ещё открыта"}]}`))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer site.Close()
	ts, srv := newTestServer(t)
	srv.ratingAPIBase = site.URL
	c := registerUser(t, srv, ts, 770910, "relay")

	resp := c.do("POST", "/api/rating/appeals", map[string]any{"tournament_id": 7, "token": "rt_1"})
	mustStatus(t, resp, 200)
	var got ratingAppealsResponse
	c.decode(resp, &got)
	if string(got.Appeals) != `[{"id":1,"questionNumber":3}]` || string(got.Controversials) != `[{"id":9,"questionNumber":3}]` || got.ControversialsError != "" {
		t.Fatalf("relay = %s %s %q", got.Appeals, got.Controversials, got.ControversialsError)
	}
	if !strings.Contains(seen[0], "Bearer rt_1") {
		t.Errorf("the token did not reach the site: %q", seen[0])
	}

	resp = c.do("POST", "/api/rating/appeals", map[string]any{"tournament_id": 8})
	mustStatus(t, resp, 200)
	c.decode(resp, &got)
	if string(got.Controversials) != "[]" || got.ControversialsError == "" {
		t.Fatalf("refused controversials = %s %q", got.Controversials, got.ControversialsError)
	}

	resp = c.do("POST", "/api/rating/verdict", map[string]any{"appeal_id": 1, "status": "A", "comment": "Вердикт: принять (3:0)", "token": "rt_1"})
	mustStatus(t, resp, 200)
	var verdict map[string]any
	c.decode(resp, &verdict)
	var sent map[string]string
	_ = json.Unmarshal([]byte(seen[len(seen)-1][strings.Index(seen[len(seen)-1], "{"):]), &sent)
	if verdict["status"] != "A" || sent["comment"] != "Вердикт: принять (3:0)" || sent["status"] != "A" {
		t.Fatalf("verdict = %v, sent %v", verdict, sent)
	}

	resp = c.do("POST", "/api/rating/verdict", map[string]any{"appeal_id": 2, "status": "D", "comment": "", "token": "rt_1"})
	mustStatus(t, resp, 400)
	if b, _ := io.ReadAll(resp.Body); !strings.Contains(string(b), "подача ещё открыта") {
		t.Fatalf("refusal = %q", b)
	}
	mustStatus(t, c.do("POST", "/api/rating/verdict", map[string]any{"appeal_id": 1, "status": "A", "comment": ""}), 400)
}
