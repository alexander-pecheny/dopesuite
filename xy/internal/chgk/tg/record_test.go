package tg

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"xy/internal/chgk/fsource"
)

// TestExportIsRecorded runs a two-tour packet with polls through the export
// and reads back what the record says: one row per message, each with the id
// the poster handed out, its question and its role, and a finished run.
func TestExportIsRecorded(t *testing.T) {
	src, err := os.ReadFile("testdata/tours.4s")
	if err != nil {
		t.Fatal(err)
	}
	polls, err := DefaultPollConfig()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "telegram.db")
	target := Target{ChannelID: "-1001111111111", ChatID: "-1002222222222"}
	ctx := context.Background()
	j, err := StartExport(ctx, path, ExportInfo{
		ToolVersion: "test", SourcePath: "testdata/tours.4s", Source: src, BotID: 42, Target: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	p := &idPoster{}
	req := Request{Doc: fsource.Parse(string(src), "chgk"), Target: target, Polls: polls, Log: j}
	if err := Export(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	if err := j.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	j.Close()

	db := openForTest(t, path)
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != journalVersion {
		t.Errorf("user_version = %d, %v", version, err)
	}
	var tool, sum, finished string
	var bot int64
	if err := db.QueryRow("SELECT tool, source_sha256, bot_id, finished_at FROM exports").
		Scan(&tool, &sum, &bot, &finished); err != nil {
		t.Fatal(err)
	}
	if tool != toolName || len(sum) != 64 || bot != 42 || finished == "" {
		t.Errorf("export row: tool=%q sha=%q bot=%d finished=%q", tool, sum, bot, finished)
	}

	rows, err := db.Query("SELECT message_id, role, COALESCE(question_number, ''), chat_id FROM posts ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		id           int64
		role, number string
		chat         string
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.role, &r.number, &r.chat); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if len(got) != len(p.sent) {
		t.Fatalf("%d rows for %d messages sent", len(got), len(p.sent))
	}
	for i, r := range got {
		if r.id != p.sent[i] {
			t.Errorf("row %d: message %d, poster sent %d", i, r.id, p.sent[i])
		}
	}
	count := map[string]int{}
	numbered := map[string]bool{}
	for _, r := range got {
		count[r.role]++
		if r.role == RoleQuestion {
			numbered[r.number] = true
		}
	}
	// The title, each tour's heading (a tour flushes what came before it),
	// four questions, a poll after each question, each tour and the packet,
	// and the index.
	want := map[string]int{RoleHeading: 3, RoleQuestion: 4, RolePoll: 7, RoleNavigation: 1}
	for role, n := range want {
		if count[role] != n {
			t.Errorf("%s posts: %d, want %d (all: %v)", role, count[role], n, count)
		}
	}
	for _, n := range []string{"1", "2", "3", "4"} {
		if !numbered[n] {
			t.Errorf("question %s has no row", n)
		}
	}
}

// TestJournalLeavesTheInboxEmpty: the record carries chgksuite's inbox tables,
// so either tool can open the file, and this one writes nothing to them.
func TestJournalLeavesTheInboxEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram.db")
	j, err := StartExport(context.Background(), path, ExportInfo{Target: Target{ChannelID: "-1001"}})
	if err != nil {
		t.Fatal(err)
	}
	j.Close()
	db := openForTest(t, path)
	for _, table := range []string{"messages", "bot_status"} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows, %v", table, n, err)
		}
	}
	var finished sql.NullString
	if err := db.QueryRow("SELECT finished_at FROM exports").Scan(&finished); err != nil || finished.Valid {
		t.Errorf("an unfinished run reads finished: %v %v", finished, err)
	}
}

// TestResolveCacheRoundTrip writes names to resolve.db and reads them back the
// way chgksuite keeps them: a channel without its "-100", and a group written
// by chgksuite with it read as the same id.
func TestResolveCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := loadResolveCache(); len(got) != 0 {
		t.Fatalf("no file, yet %v", got)
	}
	loadResolveCache().save(map[string]int64{"channel": -1001234567890})

	db := openForTest(t, filepath.Join(home, ".chgksuite", "resolve.db"))
	var stored int64
	if err := db.QueryRow("SELECT id FROM resolve WHERE username = 'channel'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1234567890 {
		t.Errorf("channel stored as %d, want it without -100", stored)
	}
	if _, err := db.Exec("INSERT INTO resolve (username, id) VALUES ('group', -1009876543210)"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	cache := loadResolveCache()
	if cache["channel"] != 1234567890 || cache["group"] != 9876543210 {
		t.Errorf("read back %v", cache)
	}
	if prefixed(cache["group"]) != "-1009876543210" {
		t.Errorf("group posts to %s", prefixed(cache["group"]))
	}
}

// idPoster numbers every message it is handed, polls included, and remembers
// the numbers in order.
type idPoster struct {
	next int64
	sent []int64
}

func (p *idPoster) id() int64 {
	p.next++
	p.sent = append(p.sent, p.next)
	return p.next
}

func (p *idPoster) PostRich(context.Context, string, string, []Media, int64) (int64, error) {
	return p.id(), nil
}

func (p *idPoster) PostText(context.Context, string, string, int64) (int64, error) {
	return p.id(), nil
}

func (p *idPoster) DiscussionMessage(context.Context, string, int64) (int64, error) {
	return 0, nil
}

func (p *idPoster) PostPoll(context.Context, map[string]any) (int64, error) { return p.id(), nil }

func (p *idPoster) Call(context.Context, string, map[string]any) error { return nil }

func openForTest(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
