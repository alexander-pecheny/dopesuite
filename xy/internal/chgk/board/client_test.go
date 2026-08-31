package board

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"xy/internal/xycli"
)

// The client is checked against a server that answers the way xy's
// trello_compat.go does — every field a real ciphertext envelope under the
// fixture's own board key, which is the corpus xycli's crypto is parity-tested
// on. Nothing here talks to a live board.

func fixtureKey(t *testing.T) (xycli.Keymeta, xycli.DataKey, string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Passphrase string        `json:"passphrase"`
		Keymeta    xycli.Keymeta `json:"keymeta"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	dk, err := xycli.Unlock(fx.Passphrase, fx.Keymeta)
	if err != nil {
		t.Fatal(err)
	}
	return fx.Keymeta, dk, fx.Passphrase
}

func TestFetchDecryptsAnXYBoard(t *testing.T) {
	km, dk, passphrase := fixtureKey(t)
	enc := func(s string) string {
		out, err := dk.EncField(s)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.URL.Query().Get("token")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keymeta": km,
			"lists":   []map[string]any{{"id": "10", "name": enc("Тур 1")}},
			"cards": []map[string]any{{
				"id": "100", "idList": "10", "desc": enc("? Вопрос?\n! Ответ."),
				"labels": []map[string]any{{"name": enc("метка")}},
			}},
		})
	}))
	defer srv.Close()

	b := Board{Service: XY, Host: "test", ID: "2", BaseURL: srv.URL, Token: "tok", Passphrase: passphrase}
	j, err := NewClient().Fetch(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if gotToken != "tok" {
		t.Errorf("token sent = %q", gotToken)
	}
	if len(j.Lists) != 1 || j.Lists[0].Name != "Тур 1" {
		t.Fatalf("lists = %+v", j.Lists)
	}
	if len(j.Cards) != 1 || j.Cards[0].Desc != "? Вопрос?\n! Ответ." {
		t.Fatalf("cards = %+v", j.Cards)
	}
	if j.Cards[0].Labels[0].Name != "метка" {
		t.Errorf("label = %q", j.Cards[0].Labels[0].Name)
	}
}

func TestFetchRejectsTheWrongPassphrase(t *testing.T) {
	km, _, _ := fixtureKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keymeta": km})
	}))
	defer srv.Close()
	b := Board{Service: XY, BaseURL: srv.URL, ID: "2", Token: "tok", Passphrase: "не тот пароль"}
	if _, err := NewClient().Fetch(context.Background(), b); err == nil {
		t.Error("a wrong passphrase should not open the board")
	}
}

func TestPostCardEncryptsForXYAndNotForTrello(t *testing.T) {
	km, dk, passphrase := fixtureKey(t)
	var posted url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"keymeta": km})
			return
		}
		_ = r.ParseForm()
		posted = r.PostForm
	}))
	defer srv.Close()

	c := NewClient()
	b := Board{Service: XY, BaseURL: srv.URL, ID: "2", Token: "tok", Passphrase: passphrase}
	if err := c.Unlock(passphrase, km); err != nil {
		t.Fatal(err)
	}
	if err := c.PostCard(context.Background(), b, "10", "Ответ", "? Вопрос?\n! Ответ."); err != nil {
		t.Fatal(err)
	}
	if posted.Get("desc") == "? Вопрос?\n! Ответ." {
		t.Fatal("the description went up in the clear")
	}
	back, err := dk.DecField(posted.Get("desc"))
	if err != nil || back != "? Вопрос?\n! Ответ." {
		t.Fatalf("round trip = %q, %v", back, err)
	}

	trello := Board{Service: Trello, BaseURL: srv.URL, ID: "b", Token: "tok", Key: "k"}
	// Point Trello's path at the same server by overriding the API base.
	c2 := NewClient()
	c2.trelloAPI = srv.URL + "/1"
	if err := c2.PostCard(context.Background(), trello, "10", "Ответ", "открытым текстом"); err != nil {
		t.Fatal(err)
	}
	if posted.Get("desc") != "открытым текстом" {
		t.Errorf("trello desc = %q", posted.Get("desc"))
	}
	if posted.Get("key") != "k" {
		t.Errorf("trello key = %q", posted.Get("key"))
	}
}
