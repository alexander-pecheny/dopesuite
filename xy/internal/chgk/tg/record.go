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

// journalVersion is the schema's PRAGMA user_version.
const journalVersion = 1

const journalSchema = `
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

// The roles a post is recorded under.
const (
	RoleHeading    = "heading"
	RoleNavigation = "navigation"
	RoleQuestion   = "question"
	RolePoll       = "poll"
	RoleOther      = "other"
)

// The kinds of message a post is.
const (
	ContentText  = "text"
	ContentPhoto = "photo"
	ContentPoll  = "poll"
)

// The parse modes a post records: parseModeHTML for sendMessage and
// sendPhoto, parseModeRichHTML for sendRichMessage, whose text is the rich HTML
// as sent. The Python tool writes the same values.
const (
	parseModeHTML     = "HTML"
	parseModeRichHTML = "rich_html"
)

// Post is one message the export sent, as the record keeps it.
type Post struct {
	ChatID    string
	MessageID int64
	// QuestionNumber is the number as the packet prints it; empty for a post
	// that is not a question's.
	QuestionNumber string
	Role           string
	ContentType    string
	ReplyTo        int64
	Text           string
	ParseMode      string
}

// PostLog is where the export reports each message once Telegram has it.
type PostLog interface {
	Posted(ctx context.Context, p Post) error
}

// ExportInfo is what the record says about a run before anything is posted.
type ExportInfo struct {
	ToolVersion string
	SourcePath  string
	Source      []byte
	// TGAccount names the bot token in chgksuite's telegram.toml; this port
	// takes its token from a flag, so it records "".
	TGAccount string
	BotID     int64
	Target    Target
}

// Journal is one export's record, open for writing.
type Journal struct {
	db       *sql.DB
	exportID int64
}

// JournalPath is the record's place, beside chgksuite's other files.
func JournalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".chgksuite", "telegram.db"), nil
}

// openShared opens one of the files this port shares with chgksuite, creating
// the folder and the schema when they are missing.
func openShared(path, schema string, version int) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
		return nil, err
	}
	return sqlitex.Open(path, func(db *sql.DB) error {
		if _, err := db.Exec(schema); err != nil {
			return err
		}
		if version == 0 {
			return nil
		}
		_, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version))
		return err
	})
}

// StartExport opens the record at path and writes the run's row.
func StartExport(ctx context.Context, path string, info ExportInfo) (*Journal, error) {
	db, err := openShared(path, journalSchema, journalVersion)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(info.SourcePath)
	if err != nil {
		abs = info.SourcePath
	}
	sum := sha256.Sum256(info.Source)
	res, err := db.ExecContext(ctx, `INSERT INTO exports
		(tool, tool_version, source_path, source_sha256, tgaccount, bot_id, channel_id, chat_id, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		toolName, info.ToolVersion, abs, hex.EncodeToString(sum[:]), info.TGAccount,
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
	return &Journal{db: db, exportID: id}, nil
}

// Posted writes one message's row, right after Telegram has accepted it, so a
// run that dies halfway still says what reached the channel.
func (j *Journal) Posted(ctx context.Context, p Post) error {
	_, err := j.db.ExecContext(ctx, `INSERT INTO posts
		(export_id, chat_id, message_id, link, question_number, role, content_type,
		 reply_to_message_id, text, parse_mode, sent_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.exportID, p.ChatID, p.MessageID, messageLink(p.ChatID, p.MessageID),
		nullString(p.QuestionNumber), p.Role, p.ContentType, nullInt(p.ReplyTo),
		p.Text, nullString(p.ParseMode), now())
	return err
}

// Finish marks the run complete. A run that never gets here keeps an empty
// finished_at.
func (j *Journal) Finish(ctx context.Context) error {
	_, err := j.db.ExecContext(ctx, `UPDATE exports SET finished_at = ? WHERE id = ?`, now(), j.exportID)
	return err
}

// Close closes the file.
func (j *Journal) Close() error { return j.db.Close() }

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
