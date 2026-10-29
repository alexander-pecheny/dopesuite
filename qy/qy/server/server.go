// Package qyserver is qy's HTTP server: the route table, the pages, the auth
// handshake, and the contest's forms. It is the trunk — it wires the mux, the
// write-transaction discipline and the background pass over new questions,
// and imports the leaf groups (domain, storage, platform, web) directly.
package qyserver

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"code.pecheny.me/pecheny/dopesuite/dopecore/sqlitex"
	"code.pecheny.me/pecheny/dopesuite/dopecore/tgbot"
	"code.pecheny.me/pecheny/dopesuite/dopecore/webassets"
	kit "pecheny.me/dopeuikit/kit"

	"qy/qy/platform/gq"
	"qy/qy/platform/openrouter"
	"qy/qy/platform/rating"
	"qy/qy/storage/store"
)

const dbFile = "qy.db"

// completer is the language model the anonymisation pass asks.
type completer interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

type server struct {
	db *sql.DB
	mu sync.Mutex // global write lock — serialises every write transaction

	assets *webassets.Assets
	pages  *kit.PageSet

	// The login bot, polling in this process (root ADR-0005); nil on an
	// instance holding no token.
	bot *tgbot.Client

	gq     gq.Source
	rating rating.Source
	// llm is nil when no OpenRouter key is set: the rule pass still runs.
	llm completer
	// wake nudges the background pass when a question arrives.
	wake chan struct{}
	now  func() time.Time
}

func openDB(path string) (*sql.DB, error) { return sqlitex.Open(path, store.Migrate) }

func newServer() (*server, error) {
	path := os.Getenv("QY_DB")
	if path == "" {
		path = dbFile
	}
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	s := &server{db: db, gq: gq.Unavailable{}, rating: rating.NewClient(), wake: make(chan struct{}, 1), now: time.Now}
	if gqPath := os.Getenv("QY_GQ_DB"); gqPath != "" {
		if s.gq, err = gq.Open(gqPath); err != nil {
			return nil, fmt.Errorf("QY_GQ_DB %s: %w", gqPath, err)
		}
	}
	if key := strings.TrimSpace(os.Getenv("QY_OPENROUTER_KEY")); key != "" {
		s.llm = openrouter.New(key, llmModel())
	}
	return s, nil
}

// defaultModel is the anonymisation model when QY_OPENROUTER_MODEL names none.
const defaultModel = "anthropic/claude-haiku-4.5"

func llmModel() string {
	if m := strings.TrimSpace(os.Getenv("QY_OPENROUTER_MODEL")); m != "" {
		return m
	}
	return defaultModel
}

// withWriteTx runs fn in one serialised write transaction under the global
// write lock (dopecore/sqlitex.Writer, which dope and spliff run on too).
func (s *server) withWriteTx(reqCtx context.Context, label string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	return sqlitex.Writer{DB: s.db, Mu: &s.mu}.Tx(reqCtx, label, fn)
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func (s *server) stamp() string { return rfc3339(s.now()) }
