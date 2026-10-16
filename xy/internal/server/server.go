// Package server is xy's HTTP server: routing, asset serving, auth, and the
// encrypted board/list/card/label/timeline/attachment API. It reuses dope's
// proven infrastructure patterns (SQLite WAL + pragmas, the conn-before-lock
// write-tx discipline, embedded assets with content-hash ETags) without
// importing dope.
package server

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"

	_ "modernc.org/sqlite"

	"code.pecheny.me/pecheny/dopesuite/dopecore/blobstore"
	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"code.pecheny.me/pecheny/dopesuite/dopecore/sqlitex"
	"code.pecheny.me/pecheny/dopesuite/dopecore/tgbot"
	"code.pecheny.me/pecheny/dopesuite/dopecore/webassets"
	kit "pecheny.me/dopeuikit/kit"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/handout"
)

const dbFile = "xy.db"

// server wires the DB, the global write lock, and the asset config.
type server struct {
	db    *sql.DB
	blobs *blobstore.Store
	mu    sync.Mutex // global write lock — serializes all write transactions

	assets *webassets.Assets

	pages *kit.PageSet // compiled ui/*.dopeui pages (see assets.go)

	staging *handoutStaging // staged handout images (see staging.go)

	// typst, compiled to wasm and run in-process (see typst.go). Built lazily and
	// shared: compiling the module is what costs, not using it. Tests inject a stub
	// so they neither compile the wasm nor need a real image to render.
	// The login bot, polling in this process (see bot.go). nil on an instance
	// that holds no token — staging, a dev checkout, a second prod binary that
	// lost the race for the poll lock.
	bot *tgbot.Client

	typstOnce sync.Once
	typst     handout.Typesetter
	typstErr  error

	// tgAPIBase is where the telegram export's own bot reaches the Bot API;
	// empty is Telegram's. Tests point it at a stub.
	tgAPIBase string

	cfg Config
	// localUser is the one account a local server has, created on first open;
	// every request is it (see local.go).
	localUser session.User
}

func openDB(path string) (*sql.DB, error) { return sqlitex.Open(path, migrate) }

// Config is what a server is built from. Main fills it from the environment;
// localserver fills it for the desktop app.
type Config struct {
	DB       string // the SQLite database file
	Blobs    string // the attachment blob directory
	BotToken string // the login bot's token; empty runs no bot
	// Local is the desktop app's mode: one user and no login, boards stored as
	// plaintext, and every route that needs a second person or a server answers
	// 404 (see local.go).
	Local bool
}

// configFromEnv is the Config prod, staging and dev have always run with.
func configFromEnv() Config {
	cfg := Config{
		DB:       os.Getenv("XY_DB"),
		Blobs:    os.Getenv("XY_BLOBS"),
		BotToken: strings.TrimSpace(os.Getenv("XY_BOT_TOKEN")),
	}
	if cfg.DB == "" {
		cfg.DB = dbFile
	}
	if cfg.Blobs == "" {
		cfg.Blobs = "blobs"
	}
	return cfg
}

func newServer(cfg Config) (*server, error) {
	db, err := openDB(cfg.DB)
	if err != nil {
		return nil, err
	}
	blobs, err := blobstore.New(cfg.Blobs)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &server{db: db, blobs: blobs, staging: newHandoutStaging(), cfg: cfg}, nil
}

// withWriteTx runs fn in one serialised write transaction under the global
// write lock. The discipline (pool wait off the lock, a bounded transaction, the
// slow-write log) is dopecore's sqlitex.Writer, which dope runs on as well.
func (s *server) withWriteTx(reqCtx context.Context, label string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	return sqlitex.Writer{DB: s.db, Mu: &s.mu}.Tx(reqCtx, label, fn)
}
