package server

import (
	"context"
	"testing"
)

// TestBoardNamePlaintext: new boards are born at schema_version 2 with a plaintext
// name and an empty name_enc, and a rename keeps them there.
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

	// DB row: plaintext name, version 2, empty name_enc.
	var name string
	var version, nameEncLen int
	if err := srv.db.QueryRow(
		`select name, schema_version, length(name_enc) from boards where id = ?`, created.ID).
		Scan(&name, &version, &nameEncLen); err != nil {
		t.Fatal(err)
	}
	if name != "Моя доска" || version != 2 || nameEncLen != 0 {
		t.Fatalf("row = (%q, v%d, name_enc %dB), want (Моя доска, v2, 0B)", name, version, nameEncLen)
	}

	// Board list + snapshot both carry the plaintext name and version.
	resp = c.do("GET", "/api/boards", nil)
	mustStatus(t, resp, 200)
	var list []boardSummary
	c.decode(resp, &list)
	if len(list) != 1 || list[0].Name != "Моя доска" || list[0].SchemaVersion != 2 {
		t.Fatalf("list = %+v, want one v2 board named Моя доска", list)
	}
	resp = c.do("GET", "/api/boards/"+bid, nil)
	mustStatus(t, resp, 200)
	var snap boardSnapshot
	c.decode(resp, &snap)
	if snap.Name != "Моя доска" || snap.SchemaVersion != 2 {
		t.Fatalf("snapshot name=%q v%d, want Моя доска v2", snap.Name, snap.SchemaVersion)
	}

	// Rename writes plaintext and stays at v2.
	resp = c.do("PATCH", "/api/boards/"+bid, map[string]string{"name": "Переименовано"})
	mustStatus(t, resp, 204)
	resp = c.do("GET", "/api/boards/"+bid, nil)
	c.decode(resp, &snap)
	if snap.Name != "Переименовано" || snap.SchemaVersion != 2 {
		t.Fatalf("after rename name=%q v%d, want Переименовано v2", snap.Name, snap.SchemaVersion)
	}
}

// TestBoardNameMigration: a legacy v1 board (name still in name_enc) is backfilled
// by POST /migrate-name, which is an idempotent no-op afterwards and never clobbers
// a subsequent rename.
func TestBoardNameMigration(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	c := registerUser(t, srv, ts, 990202, "legacy")

	// Discover the caller's user id.
	resp := c.do("GET", "/api/auth/me", nil)
	mustStatus(t, resp, 200)
	var me meResponse
	c.decode(resp, &me)

	// Seed a legacy board directly: version 1, NULL name, non-empty name_enc.
	res, err := srv.db.ExecContext(ctx, `
insert into boards(owner_user_id, name, name_enc, schema_version, kdf_salt, kdf_params, wrapped_key, verify_token, created_at, updated_at)
values(?, NULL, ?, 1, x'01', '{}', x'02', x'03', '2020-01-01T00:00:00.000Z', '2020-01-01T00:00:00.000Z')`,
		me.UserID, []byte("ciphertext-name"))
	if err != nil {
		t.Fatal(err)
	}
	legacyID, _ := res.LastInsertId()
	if _, err := srv.db.ExecContext(ctx,
		`insert into board_members(board_id, user_id, role) values(?, ?, 'owner')`, legacyID, me.UserID); err != nil {
		t.Fatal(err)
	}
	bid := itoa(legacyID)

	// Snapshot shows it as v1 with ciphertext, no plaintext name.
	resp = c.do("GET", "/api/boards/"+bid, nil)
	mustStatus(t, resp, 200)
	var snap boardSnapshot
	c.decode(resp, &snap)
	if snap.SchemaVersion != 1 || snap.Name != "" || snap.NameEnc != enc("ciphertext-name") {
		t.Fatalf("legacy snapshot = (v%d, name %q, enc %q), want v1 / empty / ciphertext", snap.SchemaVersion, snap.Name, snap.NameEnc)
	}

	// Backfill the decrypted name.
	resp = c.do("POST", "/api/boards/"+bid+"/migrate-name", map[string]string{"name": "Расшифровано"})
	mustStatus(t, resp, 204)
	resp = c.do("GET", "/api/boards/"+bid, nil)
	c.decode(resp, &snap)
	if snap.SchemaVersion != 2 || snap.Name != "Расшифровано" {
		t.Fatalf("after migrate = (v%d, %q), want v2 / Расшифровано", snap.SchemaVersion, snap.Name)
	}

	// A second migrate-name is a no-op (only touches v1 rows) — no clobber.
	resp = c.do("POST", "/api/boards/"+bid+"/migrate-name", map[string]string{"name": "Стейл"})
	mustStatus(t, resp, 204)
	resp = c.do("GET", "/api/boards/"+bid, nil)
	c.decode(resp, &snap)
	if snap.Name != "Расшифровано" {
		t.Fatalf("migrate clobbered a migrated board: name=%q", snap.Name)
	}

	// A rename followed by a stale migrate-name likewise cannot clobber.
	resp = c.do("PATCH", "/api/boards/"+bid, map[string]string{"name": "Ручное"})
	mustStatus(t, resp, 204)
	resp = c.do("POST", "/api/boards/"+bid+"/migrate-name", map[string]string{"name": "Стейл2"})
	mustStatus(t, resp, 204)
	resp = c.do("GET", "/api/boards/"+bid, nil)
	c.decode(resp, &snap)
	if snap.Name != "Ручное" {
		t.Fatalf("stale migrate clobbered a rename: name=%q", snap.Name)
	}
}
