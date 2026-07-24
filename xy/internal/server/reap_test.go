package server

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func postID(t *testing.T, c *apiClient, path string, body any) int64 {
	t.Helper()
	resp := c.do("POST", path, body)
	mustStatus(t, resp, 200)
	var v struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &v)
	return v.ID
}

func makeList(t *testing.T, c *apiClient, boardID int64, rank string) int64 {
	return postID(t, c, "/api/boards/"+itoa(boardID)+"/lists", map[string]string{"title_enc": enc("l"), "rank": rank})
}

func makeCard(t *testing.T, c *apiClient, listID int64) int64 {
	return postID(t, c, "/api/lists/"+itoa(listID)+"/cards", map[string]string{"description_enc": enc("question text"), "rank": "m"})
}

// makeBoardCard provisions board → list → card and returns their ids.
func makeBoardCard(t *testing.T, c *apiClient, name string) (boardID, listID, cardID int64) {
	t.Helper()
	bID := postID(t, c, "/api/boards", map[string]string{
		"name": name, "kdf_salt": enc("s"), "kdf_params": "{}", "wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	lID := makeList(t, c, bID, "m")
	return bID, lID, makeCard(t, c, lID)
}

func uploadAttachment(t *testing.T, c *apiClient, ts *httptest.Server, cardID int64, cipher []byte) int64 {
	t.Helper()
	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("meta", `{"filename_enc":"`+enc("f.webp")+`","mime":"image/webp"}`)
	fw, _ := mw.CreateFormFile("blob", "blob")
	fw.Write(cipher)
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/cards/"+itoa(cardID)+"/attachments", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, resp, 200)
	var att struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &att)
	return att.ID
}

func storageUsed(t *testing.T, c *apiClient) int64 {
	t.Helper()
	resp := c.do("GET", "/api/auth/storage", nil)
	mustStatus(t, resp, 200)
	var s struct {
		UsedBytes int64 `json:"used_bytes"`
	}
	c.decode(resp, &s)
	return s.UsedBytes
}

func blobRef(t *testing.T, srv *server, attID int64) string {
	t.Helper()
	var ref string
	if err := srv.db.QueryRow(`select blob_ref from attachments where id = ?`, attID).Scan(&ref); err != nil {
		t.Fatalf("blob_ref: %v", err)
	}
	return ref
}

func blobExists(t *testing.T, srv *server, ref string) bool {
	t.Helper()
	f, err := srv.blobs.Open(ref)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func TestAttachmentDeleteKeepsBlob(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 771002, "reap-att-user")
	_, _, cardID := makeBoardCard(t, c, "b")
	attID := uploadAttachment(t, c, ts, cardID, []byte("xy1-cipher"))
	ref := blobRef(t, srv, attID)

	resp := c.do("DELETE", "/api/attachments/"+itoa(attID), nil)
	mustStatus(t, resp, 204)

	if !blobExists(t, srv, ref) {
		t.Fatal("blob removed on delete; want it kept until reap")
	}
}

func countRows(t *testing.T, srv *server, table, where string, args ...any) int {
	t.Helper()
	var n int
	if err := srv.db.QueryRow(`select count(*) from `+table+` where `+where, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// The doomed board carries a list group, exercising the group_id FK on cascade.
func TestReapDestroysExpiredTombstones(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 771003, "reap-user")

	bID, l1ID, cardID := makeBoardCard(t, c, "doomed")
	attID := uploadAttachment(t, c, ts, cardID, []byte("xy1-doomed-cipher"))
	doomedRef := blobRef(t, srv, attID)
	l2ID := makeList(t, c, bID, "n")
	resp := c.do("POST", "/api/boards/"+itoa(bID)+"/list-groups", map[string]any{
		"name_enc": enc("g"), "list_ids": []int64{l1ID, l2ID},
	})
	mustStatus(t, resp, 200)

	b2ID, _, card2ID := makeBoardCard(t, c, "kept")
	att2ID := uploadAttachment(t, c, ts, card2ID, []byte("xy1-kept-cipher"))
	keptRef := blobRef(t, srv, att2ID)

	// a card tombstoned on the live board, its blob doomed with it
	card3ID := makeCard(t, c, makeList(t, c, b2ID, "n"))
	att3ID := uploadAttachment(t, c, ts, card3ID, []byte("xy1-card-doomed"))
	cardDoomedRef := blobRef(t, srv, att3ID)

	resp = c.do("DELETE", "/api/boards/"+itoa(bID), nil)
	mustStatus(t, resp, 204)
	resp = c.do("DELETE", "/api/cards/"+itoa(card3ID), nil)
	mustStatus(t, resp, 204)

	if _, err := srv.reapOnce(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("reapOnce: %v", err)
	}

	if n := countRows(t, srv, "boards", "id = ?", bID); n != 0 {
		t.Fatalf("doomed board rows = %d, want 0", n)
	}
	if n := countRows(t, srv, "cards", "board_id = ?", bID); n != 0 {
		t.Fatalf("doomed board cards = %d, want 0", n)
	}
	if n := countRows(t, srv, "cards", "id = ?", card3ID); n != 0 {
		t.Fatalf("tombstoned card rows = %d, want 0", n)
	}
	if blobExists(t, srv, doomedRef) || blobExists(t, srv, cardDoomedRef) {
		t.Fatal("doomed blobs survived reap")
	}
	if n := countRows(t, srv, "boards", "id = ?", b2ID); n != 1 {
		t.Fatalf("kept board rows = %d, want 1", n)
	}
	if !blobExists(t, srv, keptRef) {
		t.Fatal("kept blob destroyed by reap")
	}
}

func TestReapKeepsFreshTombstones(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 771004, "reap-fresh-user")
	bID, _, cardID := makeBoardCard(t, c, "fresh")
	attID := uploadAttachment(t, c, ts, cardID, []byte("xy1-fresh-cipher"))
	ref := blobRef(t, srv, attID)

	resp := c.do("DELETE", "/api/boards/"+itoa(bID), nil)
	mustStatus(t, resp, 204)

	if _, err := srv.reapOnce(context.Background(), time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("reapOnce: %v", err)
	}
	if n := countRows(t, srv, "boards", "id = ?", bID); n != 1 {
		t.Fatalf("fresh tombstoned board rows = %d, want 1", n)
	}
	if !blobExists(t, srv, ref) {
		t.Fatal("fresh tombstone's blob destroyed")
	}
}

// An expired comment tombstone that still anchors a live reply survives the
// reap (thread placeholder, watermark id stays taken); it goes once the reply
// is expired too. New comments never land on a reaped id (AUTOINCREMENT).
func TestReapKeepsThreadAnchors(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 771006, "reap-thread-user")
	_, _, cardID := makeBoardCard(t, c, "threads")

	lastCommentID := func() int64 {
		var id int64
		if err := srv.db.QueryRow(
			`select max(id) from timeline_events where card_id = ? and type = 'comment'`, cardID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	mustStatus(t, c.do("POST", "/api/cards/"+itoa(cardID)+"/comments", map[string]any{"payload_enc": enc("root")}), 204)
	rootID := lastCommentID()
	mustStatus(t, c.do("POST", "/api/cards/"+itoa(cardID)+"/comments", map[string]any{
		"payload_enc": enc("reply"), "reply_to_id": rootID,
	}), 204)
	replyID := lastCommentID()
	mustStatus(t, c.do("DELETE", "/api/comments/"+itoa(rootID), nil), 204)

	if _, err := srv.reapOnce(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("reapOnce: %v", err)
	}
	if n := countRows(t, srv, "timeline_events", "id = ?", rootID); n != 1 {
		t.Fatal("expired root with a live reply was reaped")
	}
	if n := countRows(t, srv, "timeline_events", "id = ? and reply_to_id = ?", replyID, rootID); n != 1 {
		t.Fatal("live reply lost its thread anchor")
	}

	mustStatus(t, c.do("DELETE", "/api/comments/"+itoa(replyID), nil), 204)
	if _, err := srv.reapOnce(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("second reapOnce: %v", err)
	}
	if n := countRows(t, srv, "timeline_events", "id in (?, ?)", rootID, replyID); n != 0 {
		t.Fatal("thread not reaped after all replies expired")
	}

	mustStatus(t, c.do("POST", "/api/cards/"+itoa(cardID)+"/comments", map[string]any{"payload_enc": enc("after")}), 204)
	if nextID := lastCommentID(); nextID <= replyID {
		t.Fatalf("comment id %d reuses reaped id space (max was %d)", nextID, replyID)
	}
}

func TestSweepOrphanBlobs(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 771005, "sweep-user")
	_, _, cardID := makeBoardCard(t, c, "b")
	attID := uploadAttachment(t, c, ts, cardID, []byte("xy1-referenced"))
	referenced := blobRef(t, srv, attID)

	orphan, _, err := srv.blobs.Put(bytes.NewReader([]byte("xy1-leaked")))
	if err != nil {
		t.Fatal(err)
	}

	n, err := srv.sweepOrphanBlobs(context.Background(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || !blobExists(t, srv, orphan) {
		t.Fatal("young orphan swept; want it kept inside the min-age window")
	}

	n, err = srv.sweepOrphanBlobs(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || blobExists(t, srv, orphan) {
		t.Fatalf("swept %d; want the 1 orphan gone", n)
	}
	if !blobExists(t, srv, referenced) {
		t.Fatal("referenced blob swept")
	}
}

func TestStorageCountsLiveDataOnly(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 771001, "reap-quota-user")

	bID, _, cardID := makeBoardCard(t, c, "doomed")
	uploadAttachment(t, c, ts, cardID, []byte("xy1-cipher-bytes-of-some-length"))
	_, _, card2ID := makeBoardCard(t, c, "kept")
	uploadAttachment(t, c, ts, card2ID, []byte("xy1-other-cipher"))

	before := storageUsed(t, c)
	if before == 0 {
		t.Fatal("expected non-zero storage before delete")
	}

	resp := c.do("DELETE", "/api/boards/"+itoa(bID), nil)
	mustStatus(t, resp, 204)
	afterBoard := storageUsed(t, c)
	if afterBoard >= before {
		t.Fatalf("storage did not drop after board delete: %d -> %d", before, afterBoard)
	}

	resp = c.do("DELETE", "/api/cards/"+itoa(card2ID), nil)
	mustStatus(t, resp, 204)
	afterCard := storageUsed(t, c)
	if afterCard >= afterBoard {
		t.Fatalf("storage did not drop after card delete: %d -> %d", afterBoard, afterCard)
	}
}

// A re-delete of any tombstoned entity must not re-stamp deleted_at — that
// would reset the 14-day reap clock (e.g. an offline-queue replay of an old
// delete). All delete handlers go through tombstone(), which only stamps live
// rows; this exercises that seam directly, including the list→cards cascade.
func TestTombstoneNeverRestamps(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 771006, "restamp-user")
	bID, lID, cardID := makeBoardCard(t, c, "restamp")

	const old = "2000-01-01T00:00:00Z"
	ctx := context.Background()
	stamp := func(table, where string, id int64) {
		t.Helper()
		if err := srv.withWriteTx(ctx, "test-tombstone", func(ctx context.Context, tx *sql.Tx) error {
			return tombstone(ctx, tx, table, where, id)
		}); err != nil {
			t.Fatalf("tombstone %s: %v", table, err)
		}
	}
	deletedAt := func(table string, id int64) string {
		t.Helper()
		var v string
		if err := srv.db.QueryRow(`select deleted_at from `+table+` where id = ?`, id).Scan(&v); err != nil {
			t.Fatalf("deleted_at %s: %v", table, err)
		}
		return v
	}

	for _, e := range []struct {
		table, where string
		id           int64
	}{
		{"cards", "id = ?", cardID},
		{"lists", "id = ?", lID},
		{"boards", "id = ?", bID},
	} {
		stamp(e.table, e.where, e.id)
		if _, err := srv.db.Exec(`update `+e.table+` set deleted_at = ? where id = ?`, old, e.id); err != nil {
			t.Fatal(err)
		}
		stamp(e.table, e.where, e.id)
		if got := deletedAt(e.table, e.id); got != old {
			t.Fatalf("%s re-delete reset the reap clock: deleted_at = %s, want %s", e.table, got, old)
		}
	}

	// The list-delete cascade must skip cards already tombstoned.
	stamp("cards", "list_id = ?", lID)
	if got := deletedAt("cards", cardID); got != old {
		t.Fatalf("cascade re-delete reset the reap clock: deleted_at = %s, want %s", got, old)
	}
}
