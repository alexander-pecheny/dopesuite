package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/xycli"
)

// The CLI end to end: an API token authorizes it, a passphrase unlocks a board,
// and a card written through it comes back readable — with the лента entry the
// browser would have written. The board's keymeta comes from the parity corpus
// crypto.ts generated, so the passphrase here is the browser's own.
func TestXYCLIEndToEnd(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 555200, "cliuser")
	token := mintToken(t, c, "xy-cli")

	raw, err := os.ReadFile("../xycli/testdata/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Passphrase string          `json:"passphrase"`
		Keymeta    map[string]any  `json:"keymeta"`
		TSSealed   json.RawMessage `json:"ts_sealed"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"name": "доска для CLI"}
	for k, v := range fixture.Keymeta {
		body[k] = v
	}
	resp := c.do("POST", "/api/boards", body)
	mustStatus(t, resp, 200)
	var created struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &created)
	boardID := itoa(created.ID)

	t.Setenv("XY_CLI_STATE", filepath.Join(t.TempDir(), "state.json"))
	cli := func(stdin string, args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := xycli.Run(args, strings.NewReader(stdin), &out, &errOut); code != 0 {
			t.Fatalf("xy-cli %s: exit %d\n%s", strings.Join(args, " "), code, errOut.String())
		}
		return out.String()
	}

	cli("", "login", "--url", ts.URL, "--token", token)
	cli(fixture.Passphrase+"\n", "unlock", boardID)

	// A list, then a question in it.
	var list struct {
		ID int64 `json:"id"`
	}
	mustJSON(t, cli("", "list", "add", "--board", boardID, "--title", "Тур 1", "--json"), &list)
	var card struct {
		ID int64 `json:"id"`
	}
	question := "? Что открывает конверт?\n! Ключ доски\n@ Тестер"
	mustJSON(t, cli(question, "card", "add", "--board", boardID, "--list", itoa(list.ID), "--json"), &card)

	// It comes back byte for byte, with the hash --expect compares.
	var got struct {
		Desc string `json:"desc"`
		Hash string `json:"hash"`
	}
	mustJSON(t, cli("", "card", "get", itoa(card.ID), "--board", boardID, "--json"), &got)
	if got.Desc != question {
		t.Fatalf("card get = %q, want %q", got.Desc, question)
	}

	// A stale --expect refuses; the fresh one writes.
	var out, errOut bytes.Buffer
	if code := xycli.Run([]string{"card", "set", itoa(card.ID), "--board", boardID, "--expect", "0badc0ffee11"},
		strings.NewReader("? Другой вопрос\n! Ответ"), &out, &errOut); code == 0 {
		t.Fatal("card set with a stale --expect should refuse")
	}
	edited := "? Что открывает конверт?\n! Ключ доски, и ничего больше\n@ Тестер"
	cli(edited, "card", "set", itoa(card.ID), "--board", boardID, "--expect", got.Hash)

	// The лента records what the question used to say.
	resp = c.do("GET", "/api/cards/"+itoa(card.ID)+"/timeline", nil)
	mustStatus(t, resp, 200)
	var events []timelineEventDTO
	c.decode(resp, &events)
	if len(events) != 1 || events[0].Type != "desc_edit" {
		t.Fatalf("timeline = %+v, want one desc_edit", events)
	}

	// A comment naming a member resolves to a mention, not just text.
	cli("@cliuser зачёт бы подробнее", "comment", "add", itoa(card.ID), "--board", boardID)
	comments := cli("", "comment", "ls", itoa(card.ID), "--board", boardID)
	if !strings.Contains(comments, "зачёт бы подробнее") {
		t.Fatalf("comment ls = %q", comments)
	}
	var mentions int
	if err := srv.db.QueryRow(`select count(*) from event_mentions`).Scan(&mentions); err != nil {
		t.Fatal(err)
	}
	if mentions != 1 {
		t.Fatalf("mentions = %d, want 1", mentions)
	}

	// Search folds: «зачет» finds the «зачёт» that was typed.
	hits := cli("", "search", "зачет", "--board", boardID)
	if !strings.Contains(hits, "зачёт бы подробнее") {
		t.Fatalf("search = %q", hits)
	}

	// A label, put on the card, leaves its own лента entry.
	var label struct {
		ID int64 `json:"id"`
	}
	mustJSON(t, cli("", "label", "add", "--board", boardID, "--name", "готово", "--json"), &label)
	cli("", "label", "assign", itoa(card.ID), "--board", boardID, "--label", "готово")
	if labels := cli("", "label", "ls", "--board", boardID, "--card", itoa(card.ID)); !strings.Contains(labels, "готово") {
		t.Fatalf("label ls --card = %q", labels)
	}
	resp = c.do("GET", "/api/cards/"+itoa(card.ID)+"/timeline", nil)
	mustStatus(t, resp, 200)
	c.decode(resp, &events)
	if !hasEvent(events, "label_add") {
		t.Fatalf("timeline = %+v, want a label_add", events)
	}

	// An attachment round-trips through the envelope: what is uploaded is what
	// comes back, and the server held only ciphertext in between.
	dir := t.TempDir()
	picture := filepath.Join(dir, "раздатка.txt")
	if err := os.WriteFile(picture, []byte("это раздатка"), 0o644); err != nil {
		t.Fatal(err)
	}
	var att struct {
		ID int64 `json:"id"`
	}
	mustJSON(t, cli("", "attachment", "add", itoa(card.ID), "--board", boardID, picture, "--json"), &att)
	if att.ID == 0 {
		t.Fatal("attachment add returned no id")
	}
	back := filepath.Join(dir, "скачано.txt")
	cli("", "attachment", "get", itoa(att.ID), "--board", boardID, "-o", back)
	if got, err := os.ReadFile(back); err != nil || string(got) != "это раздатка" {
		t.Fatalf("attachment get = %q, %v", got, err)
	}

	// The list's source is the card's 4s, as an export would assemble it.
	if source := cli("", "source", "--board", boardID, "--list", itoa(list.ID)); source != edited+"\n" {
		t.Fatalf("source = %q, want %q", source, edited+"\n")
	}
}

func mustJSON(t *testing.T, raw string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
}

func hasEvent(events []timelineEventDTO, kind string) bool {
	for _, e := range events {
		if e.Type == kind {
			return true
		}
	}
	return false
}
