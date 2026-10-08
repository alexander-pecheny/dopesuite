package tg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	// The driver behind sqlitex's "sqlite" name; the CLI links nothing else
	// that would register it.
	_ "modernc.org/sqlite"

	"pecheny.me/dopecore/sqlitex"
)

// Every export leaves a record of what it posted in ~/.chgksuite/telegram.db:
// the run, and one row per message with its id, its question and the text it
// was sent with. That is what lets a post be found and corrected after the
// fact. chgksuite writes the same file with the same schema — the schema is a
// contract between the two tools, described in docs/telegram-db.md — and its
// bot uses the messages and bot_status tables as its inbox. This port's bot
// keeps its inbox in memory, so it creates those two tables and leaves them
// empty.

// recordVersion is the schema's PRAGMA user_version.
const recordVersion = 1

const recordSchema = `
CREATE TABLE IF NOT EXISTS messages (raw_data TEXT, chat_id TEXT, created_at TEXT);
CREATE TABLE IF NOT EXISTS bot_status (raw_data TEXT, created_at TEXT);
CREATE TABLE IF NOT EXISTS exports (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tool TEXT NOT NULL,
  tool_version TEXT NOT NULL,
  source_path TEXT NOT NULL,
  source_sha256 TEXT NOT NULL,
  tgaccount TEXT NOT NULL,
  bot_id INTEGER,
  channel_id TEXT NOT NULL,
  chat_id TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT
);
CREATE TABLE IF NOT EXISTS posts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  export_id INTEGER NOT NULL REFERENCES exports(id),
  chat_id TEXT NOT NULL,
  message_id INTEGER NOT NULL,
  link TEXT,
  question_number TEXT,
  role TEXT NOT NULL,
  content_type TEXT NOT NULL,
  reply_to_message_id INTEGER,
  text TEXT,
  parse_mode TEXT,
  entities TEXT,
  sent_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS posts_export ON posts(export_id);
CREATE INDEX IF NOT EXISTS messages_created ON messages(created_at);
`

// toolName is how this port signs its rows in exports.tool; chgksuite signs
// its own "chgksuite".
const toolName = "chgksuite-go"

// Role is what a post is, in posts.role. chgksuite also writes "answer",
// "comment" and "handout"; this port folds those into the question's message.
type Role string

const (
	RoleHeading    Role = "heading"
	RoleNavigation Role = "navigation"
	RoleQuestion   Role = "question"
	RolePoll       Role = "poll"
	RoleOther      Role = "other"
)

// ContentType is the kind of message a post is, in posts.content_type.
type ContentType string

const (
	ContentText  ContentType = "text"
	ContentPhoto ContentType = "photo"
	ContentPoll  ContentType = "poll"
)

// ParseMode is how a post's text is marked up, in posts.parse_mode:
// parseModeHTML for sendMessage and sendPhoto, parseModeRichHTML for
// sendRichMessage, whose text is the rich HTML as sent. chgksuite writes the
// same values; a poll has none.
type ParseMode string

const (
	parseModeHTML     ParseMode = "HTML"
	parseModeRichHTML ParseMode = "rich_html"
)

// Post is one message the export sent, as the record keeps it.
type Post struct {
	ChatID    string
	MessageID int64
	// QuestionNumber is the number as the packet prints it; empty for a post
	// that is not a question's.
	QuestionNumber string
	Role           Role
	ContentType    ContentType
	ReplyTo        int64
	Text           string
	ParseMode      ParseMode
}

// Recorder is what the export tells about each message once Telegram has it;
// a *Record in the CLI, a fake in tests.
type Recorder interface {
	Posted(ctx context.Context, p Post) error
}

// ExportInfo is what the record says about a run before anything is posted.
type ExportInfo struct {
	ToolVersion string
	SourcePath  string
	Source      []byte
	BotID       int64
	Target      Target
}

// Record is one export's rows in telegram.db, open for writing.
type Record struct {
	db       *sql.DB
	exportID int64
}

// sharedPath is a file in ~/.chgksuite, the folder this port shares with
// chgksuite.
func sharedPath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".chgksuite", name), nil
}

// RecordPath is where telegram.db lives.
func RecordPath() (string, error) { return sharedPath("telegram.db") }

// OpenRecord opens telegram.db at path, creating it when it is missing, and
// writes the run's row in exports.
func OpenRecord(ctx context.Context, path string, info ExportInfo) (*Record, error) {
	if err := os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
		return nil, err
	}
	db, err := sqlitex.Open(path, func(db *sql.DB) error {
		if _, err := db.Exec(recordSchema); err != nil {
			return err
		}
		_, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", recordVersion))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(info.SourcePath)
	if err != nil {
		abs = info.SourcePath
	}
	sum := sha256.Sum256(info.Source)
	// tgaccount names a bot token in chgksuite's telegram.toml. This port
	// takes its token from --token, so it has no account name and writes ''.
	res, err := db.ExecContext(ctx, `INSERT INTO exports
		(tool, tool_version, source_path, source_sha256, tgaccount, bot_id, channel_id, chat_id, started_at)
		VALUES (?, ?, ?, ?, '', ?, ?, ?, ?)`,
		toolName, info.ToolVersion, abs, hex.EncodeToString(sum[:]),
		nullInt(info.BotID), info.Target.ChannelID, nullString(info.Target.ChatID), now())
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Record{db: db, exportID: id}, nil
}

// Posted writes one message's row, right after Telegram has accepted it, so a
// run that dies halfway still says what reached the channel.
func (r *Record) Posted(ctx context.Context, p Post) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO posts
		(export_id, chat_id, message_id, link, question_number, role, content_type,
		 reply_to_message_id, text, parse_mode, sent_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.exportID, p.ChatID, p.MessageID, messageLink(p.ChatID, p.MessageID),
		nullString(p.QuestionNumber), string(p.Role), string(p.ContentType), nullInt(p.ReplyTo),
		p.Text, nullString(string(p.ParseMode)), now())
	return err
}

// Finish marks the run complete. A run that never gets here keeps an empty
// finished_at.
func (r *Record) Finish(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `UPDATE exports SET finished_at = ? WHERE id = ?`, now(), r.exportID)
	return err
}

// Close closes the file.
func (r *Record) Close() error { return r.db.Close() }

// now is in UTC with microseconds, as the Python tool writes it, so the times
// of both tools compare as strings.
func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00") }

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
