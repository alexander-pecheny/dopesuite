// Package core holds the server's shared runtime state and the primitives that
// operate on it (the DB handle, the write-transaction machinery, broadcasting).
// It is being grown incrementally as part of the server→shell restructure: state
// fields move here field-by-field, then the methods that use them follow, so the
// dopeserver handlers can eventually become functions taking *core.Engine and
// move into their own ≤10-file packages.
package core

import (
	"database/sql"
	"io/fs"
	"sync"
	"sync/atomic"

	"dope/dope/realtime"
	"dope/dope/store"
)

// FestScope identifies a fest (and optionally a game within it) — the URL-derived
// addressing pair threaded through the scoped API and the host/viewer handlers.
type FestScope struct {
	FestID int64
	GameID int64
}

// Engine is the shared server runtime state. The zero value is usable (so a
// server built directly in tests is safe); production wiring fills the fields.
type Engine struct {
	// DB is the shared SQLite handle (WAL mode).
	DB *sql.DB
	// FestID/ActiveGameID/ActiveMatchCode identify the process's active match
	// (the legacy single-match pointer, still used by the EK host surface).
	FestID          int64
	ActiveGameID    int64
	ActiveMatchCode string
	// State is the in-memory match state for the active match.
	State store.MatchState
	// RT is the SSE publisher (subscriber registry + broadcast fan-out, with its
	// own locks independent of the write mutex).
	RT *realtime.Manager
	// Epoch is the per-process random token stamped on every SSE envelope/init so
	// clients detect a restart (seq reset) and resync.
	Epoch string
	// BotSecret gates the Telegram bridge endpoints; empty disables them.
	BotSecret string
	// Assets is the embedded (or disk, in dev) static asset filesystem.
	Assets fs.FS
	// AssetNoCache is true in disk/dev mode (assets served no-cache).
	AssetNoCache bool
	// AssetETags maps "/static/..." paths to content-hash ETags for cache-busting
	// (nil in disk mode).
	AssetETags map[string]string

	// Mu guards game/DB writes (writers win contention over viewer reads).
	Mu sync.RWMutex
	// FestViewMu guards FestViewCache; FestViewCache holds JSON FestView responses
	// keyed by (festID, gameID), invalidated per-fest on broadcast.
	FestViewMu    sync.RWMutex
	FestViewCache map[int64]map[int64][]byte
	// SeqMu serialises per-scope seq assignment + fan-out; StateSeq is the
	// per-scope monotonic SSE counter.
	SeqMu    sync.Mutex
	StateSeq map[string]uint64
	// DeltaBuf coalesces scoped delta broadcasts: rapid edits to one scope buffer
	// their ops within deltaCoalesceWindow and fan out to viewers as one merged
	// delta. Guarded by SeqMu. (See broadcast.go.)
	DeltaBuf map[string]*pendingDelta

	// Static ("DDoS lockdown") mode gauges/state. StaticMode is the effective
	// state; StaticManual the override. ReqRate/SseConns/InFlight feed the
	// auto-trigger; LiveFallthrough caps cookie-bearing requests under lockdown.
	StaticMode      atomic.Bool
	StaticManual    atomic.Int32
	InFlight        atomic.Int64
	ReqRate         atomic.Int64
	LastRate        atomic.Int64
	SseConns        atomic.Int64
	LiveFallthrough atomic.Int64
}
