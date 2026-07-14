# Architecture & package layout

This document describes how the Go code is organised. It complements the
file-by-file map in `AGENTS.md`.

## Why this exists

The server started life as a single flat `package main` (~70 files, ~32K lines):
game-specific logic, persistence, the realtime/SSE layer and the HTTP handlers
all lived side by side, and game-type knowledge (`"ek"`/`"od"`/`"ksi"`/`"si"`
literals and `switch gameType` blocks) was scattered across a dozen files. With
more game formats coming (10–15 expected), that sprawl only got worse.

The code was first decomposed into ~30 leaf packages, then those packages were
grouped into **seven semantic top-level directories** so the tree is navigable
at a glance. The inner `dope/` directory now contains **no loose `.go` files** —
every file lives in a package, and every package lives in one of the seven
groups.

## Layout

Module root is the repo root (`go.mod: module "dope"`); the server tree lives
under the inner `dope/` directory, so packages resolve as `dope/dope/<group>/<pkg>`.

```
dope/                       module root (go.mod: module "dope")
  dope/                     server tree — seven semantic groups, no loose files:
    cmd/                    entry points
      dope-server/          thin main() → dopeserver.Main()
      telegram-bot/         standalone Telegram bot (bridges to the server)
    server/                 package dopeserver — orchestration / the trunk
      tests/                black-box integration tests (package tests)
    web/                    HTTP / UI layer
    domain/                 game + festival domain logic
    storage/                persistence
    export/                 output generation (xlsx / json / archive)
    platform/               cross-cutting leaves (stdlib-only or near it)
```

### `server/` — the orchestration trunk (package `dopeserver`)

The one package that wires everything together: the HTTP mux, the write-tx
discipline, SSE driving, and the request handlers that don't belong to a leaf.
It imports the groups below directly (no re-export shims). Split into cohesive
files, each owning one concern:

| File | Concern |
|------|---------|
| `main.go` | entry point, mux wiring, HTTP server, SSE event handlers |
| `db.go` | DB bootstrap, schema migration/backfill, id resolution |
| `serve_html.go` | host/viewer/game HTML init payloads + asset versioning |
| `import_scheme.go` | fest-scheme import handlers |
| `matchview.go` | fest/match view loading + match-update application |
| `scoped_api.go` | tournament-scoped API endpoints |
| `auth.go` | sessions, auth, Telegram login bridge |
| `credentials.go` | invite/session/Telegram code + name helpers |
| `pages_public.go` | public viewer-page routing |
| `static_mode.go` | "DDoS lockdown" static-snapshot degradation layer |
| `host_accessors.go` | **dependency-inversion adapter** — see below |
| `testapi.go` | the single exported test seam for `server/tests/` |

**Dependency inversion (`host_accessors.go`).** Handler/leaf packages
(`web/pages`, `web/hostpages`, `web/telegrambridge`, `export/gameexport`,
`domain/overrides`) declare narrow `Host` interfaces describing what they need
from the server. `*server` satisfies them through thin accessors collected in
`host_accessors.go` (`var _ gameexport.Host = (*server)(nil)`, …). This keeps
those packages cycle-free leaves while preserving server encapsulation — and it
is what made the decomposition possible without import cycles.

### `web/` — HTTP / UI layer

- `pages` — public + admin page handlers (register, admin, host journal/numbers).
- `hostpages` — host editor page handlers (dashboard, roster, numbers, games).
- `editbatch` — coalesces per-game PATCH edits into one locked write tx per window.
- `telegrambridge` — shared-secret endpoints the bot calls instead of opening the DB.
- `assets` — the `//go:embed static` package; the FS keeps the `static/` prefix.
  Frontend source lives under `web/assets/static/`; Deno tests under `web/jstest/`.

### `domain/` — game + festival domain logic

The home for **pure** per-game and festival logic. Logic that needs a DB
transaction or the server stays in `dopeserver` and calls into here for type
metadata.

- `games` — the single source of truth for game-type knowledge: canonical codes
  (`EK`, `OD`, `KSI`, `SI`), the `Definition` registry, and the pure OD (ЧГК)
  domain (state shapes, tour-composition parsing, standings scoring). Add a new
  format by registering a `Definition` — not by adding another `switch gameType`.
- `core` — the `Engine`: shared in-memory state, write-tx plumbing, journal
  service, broadcast, revert. Embedded by `*server`.
- `resolver` — bracket/reseed resolution. `roster` — roster + seeding.
- `overrides` — player-name overrides. `imports` — EK/seed/rating bulk import.
- `numbering` — team-number assignment. `edit` — match-edit value types.
- `view` — shared presentation DTOs (e.g. `HostFest`) kept in a leaf so the
  server and the web handlers can name them without importing each other.

### `storage/` — persistence

- `store` — SQLite schema, query helpers, and the shared view/scheme types
  (`MatchView`, `FestView`, `FestScheme`, …) plus pure scoring (`BuildView`,
  `ScoreTeam`, `ManualStandings`). Almost everything depends on it.
- `journal` — the forward-journal subsystem: on-disk codec, replay/checkpoint
  engines, hot→cold archiver, live append/read path.
- `migrate` — audit/history *data* conversion + maintenance subcommands.
- `festwrite` — the attribution-aware write/append facade.
- `festaccess` — per-fest access/role persistence (DB-backed authz).
- `auditmw` — audit-log write middleware. `storeutil` — scheme/query helpers.
- `sqlitez` — low-level SQLite helpers.

### `export/` — output generation

- `xlsxexport` — per-game xlsx sheet builders (OD/KSI/EK).
- `gameexport` — game export orchestration (xlsx / json / results archive).

### `platform/` — cross-cutting leaves

Stdlib-only or near-it utilities with no domain knowledge:

- `realtime` — SSE envelopes, delta merging, the subscriber `Manager`.
- `roles` — role hierarchy + pure permission predicates.
- `markdown` — goldmark wrapper rendering host markdown to safe HTML.
- `session` — session-token types. `metrics` — edit-path instrumentation.
- `util` — small shared helpers.

## Guiding principles

- **Leaf-first, no cycles.** A package must not import `server` (`package
  dopeserver`). If it needs something from the trunk, that something moves down
  with it, or is passed in via a narrow `Host` interface (see
  `host_accessors.go`).
- **Strict downward layering.** Edges flow
  `cmd → server → web → domain/storage/platform`, and within the lower groups
  `domain → storage → platform`. Nothing in `domain/`, `storage/`, `platform/`,
  `export/` imports `web/` or `server/`. `platform/` imports no internal package
  upward of itself.
- **Registry over switches.** New game-type behaviour is registered in
  `domain/games`, not added as another `switch gameType` in a handler.
- **Behaviour-preserving refactors.** Verified by the existing test suite
  (`just test`) plus `just vet`; no functional change rides along.

## Concurrency & outage hardening

Two locks, separated so views can never stall edits:

- `s.mu` (RWMutex) guards game/DB writes. Writes take `Lock`; the few read
  paths that need a consistent in-memory view take `RLock`. Go's RWMutex gives a
  waiting writer priority over new readers, so **edits win contention** with
  viewer reads.
- `s.subMu` guards the SSE subscriber maps, independently of `s.mu`. Broadcast
  fan-out snapshots the channel list under `subMu.RLock`, releases it, then sends
  — and every send is non-blocking (buffered channel + drop-oldest). So a slow or
  dead spectator can never block the broadcaster or an editor.

**Write-lock discipline (the 2026-06-13 freeze).** A write must never wait for a
pooled DB connection *while holding `s.mu`*: the pool has only
`sqliteMaxOpenConns` (8) connections, shared with viewer reads, so a starved
pool could otherwise pin the lock indefinitely and freeze the whole site (it did,
for ~55 min). Every write therefore:

1. acquires its connection BEFORE the lock (`acquireWriteConn`, off-lock), and
2. bounds the whole transaction with `writeTxTimeout` (5s) via
   `auditDetachedContext`, so even off-lock it can never wait forever.

Use `s.withWriteTx(reqCtx, festID, label, fn)` for this — it encapsulates the
pattern (conn off-lock → `lockWrite` → `beginWriteTxConn` → commit). Reach for
the lower-level `acquireWriteConn` / `lockWrite` / `beginWriteTxConn` trio only
when a path needs work between lock and tx, or post-commit under the lock (e.g.
updating the in-memory active-game pointer). The hot, continuously-exercised
write paths all follow this: game-state edits (batched, `web/editbatch`), match
edits, game-state PUT, journal revert/undo, player overrides, fest numbering,
game create/delete, roster/reseed/rating import. The periodic journal archiver
bounds its background pass the same way.

**Dead-connection reaping.** SSE writes (`/events`, `/host-events`) are bounded
by a per-write deadline (`http.ResponseController` + `sseWriteTimeout`); a dead
or non-consuming client errors and is removed within one keepalive, instead of
lingering and inflating the per-game viewer tally.

**Residual old-pattern writes (lower priority).** A few write paths still acquire
their connection under `s.mu` and should be migrated to `withWriteTx`/the trio
when next touched. They are deliberately deferred because they run pre-fest,
rarely, or off the viewer-load path, so the freeze window barely applies:
`handleHostClearGame` and `handleHostDeleteFest` (destructive, not run mid-play),
`importScheme` / `importSchemeIntoFest` / `importSeedsFromKSI` /
`setSeedImportDeclined` (pre-fest seeding, no concurrent viewer load), and the
telegram-bridge login/register writes (tiny single statements on the bot path).
