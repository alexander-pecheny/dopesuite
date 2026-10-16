package server

import (
	"strings"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/dopecore/schema"
)

// TestBoardNamePlaintext: a board's name is plaintext in the row, the list and
// the snapshot, and a rename writes plaintext too.
func TestBoardNamePlaintext(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 990101, "namer")

	resp := c.do("POST", "/api/boards", map[string]string{
		"name": "Моя доска", "kdf_salt": enc("s"), "kdf_params": "{}",
		"wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	mustStatus(t, resp, 200)
	var created struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &created)
	bid := itoa(created.ID)

	var name string
	if err := srv.db.QueryRow(`select name from boards where id = ?`, created.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Моя доска" {
		t.Fatalf("row name = %q, want Моя доска", name)
	}

	resp = c.do("GET", "/api/boards", nil)
	mustStatus(t, resp, 200)
	var list []boardSummary
	c.decode(resp, &list)
	if len(list) != 1 || list[0].Name != "Моя доска" {
		t.Fatalf("list = %+v, want one board named Моя доска", list)
	}
	resp = c.do("GET", "/api/boards/"+bid, nil)
	mustStatus(t, resp, 200)
	var snap boardSnapshot
	c.decode(resp, &snap)
	if snap.Name != "Моя доска" {
		t.Fatalf("snapshot name = %q, want Моя доска", snap.Name)
	}

	resp = c.do("PATCH", "/api/boards/"+bid, map[string]string{"name": "Переименовано"})
	mustStatus(t, resp, 204)
	resp = c.do("GET", "/api/boards/"+bid, nil)
	c.decode(resp, &snap)
	if snap.Name != "Переименовано" {
		t.Fatalf("after rename name = %q, want Переименовано", snap.Name)
	}
}

// TestMigrateV29KeepsContent: v29 renames the content columns and every byte in
// them stays where it was, under the new name.
func TestMigrateV29KeepsContent(t *testing.T) {
	db := migratedDBThrough(t, 28)
	uid := insertUser(t, db, "owner")
	bid := insertBoard(t, db, uid, "board")
	list := insertList(t, db, bid, "normal")
	card := insertCard(t, db, bid, list, "question", "вопрос")
	mustExec(t, db, `update cards set handout_meta_enc = 'h', alias_enc = 'a', seen_enc = 's' where id = ?`, card)
	insertLabel(t, db, bid, "метка")
	insertLegacyEvent(t, db, bid, card, "comment", "комментарий")
	insertAttachment(t, db, bid, card)
	group := execIns(t, db, `insert into list_groups(board_id, name_enc, created_at, updated_at) values(?, 'группа', ?, ?)`, bid, fixtureNow, fixtureNow)
	execIns(t, db, `insert into test_sessions(board_id, meta_enc, created_at) values(?, 'сессия', ?)`, bid, fixtureNow)
	mustExec(t, db, `insert into tour_declarations(board_id, group_id, names_enc) values(?, ?, 'тестеры')`, bid, group)

	if err := schema.Apply(db, migrations); err != nil {
		t.Fatalf("v29: %v", err)
	}
	for q, want := range map[string]string{
		`select cast(title as text) from lists`:                         "list",
		`select cast(name as text) from list_groups`:                    "группа",
		`select cast(description as text) from cards`:                   "вопрос",
		`select cast(handout_meta || alias || seen as text) from cards`: "has",
		`select cast(name || color as text) from labels`:                "метка#3aa657",
		`select cast(payload as text) from timeline_events`:             "комментарий",
		`select cast(filename as text) from attachments`:                "f.png",
		`select cast(meta as text) from test_sessions`:                  "сессия",
		`select cast(names as text) from tour_declarations`:             "тестеры",
	} {
		if got := scalarStr(t, db, q); got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}
	if got := scalarInt(t, db, `select count(*) from pragma_table_info('boards') where name in ('name_enc', 'schema_version')`); got != 0 {
		t.Errorf("boards still has %d of name_enc, schema_version", got)
	}
}

// TestMigrateV29RefusesALegacyBoard: a board whose name lives only in name_enc
// would lose it, so the step stops instead.
func TestMigrateV29RefusesALegacyBoard(t *testing.T) {
	db := migratedDBThrough(t, 28)
	uid := insertUser(t, db, "owner")
	bid := insertBoard(t, db, uid, "board")
	mustExec(t, db, `update boards set name = null, schema_version = 1 where id = ?`, bid)

	err := schema.Apply(db, migrations)
	if err == nil || !strings.Contains(err.Error(), "schema_version 1") {
		t.Fatalf("v29 on a legacy board: %v, want a refusal", err)
	}
	if got := scalarInt(t, db, `select count(*) from pragma_table_info('boards') where name = 'name_enc'`); got != 1 {
		t.Error("the refused step still dropped name_enc")
	}
}
