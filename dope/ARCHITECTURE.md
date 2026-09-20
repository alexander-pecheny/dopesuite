# Architecture & package layout

This document describes how the Go code is organised. It complements the
file-by-file map in `AGENTS.md`.

## Why this exists

The server began as a single flat `package main` of about 70 files and 32,000
lines. Game-specific logic, persistence, the realtime and SSE layer and the HTTP
handlers all sat side by side, and knowledge about game types — `"ek"`, `"od"`,
`"ksi"` and `"si"` string literals, and `switch gameType` blocks — was spread
over a dozen files. We expect 10 to 15 more formats, so that would only have got
worse.

The code was first broken up into about 30 leaf packages, and then those
packages were grouped into **seven top-level directories**, so that the tree can
be taken in at a glance. The inner `dope/` directory now has **no loose `.go`
files** at all: every file is in a package, and every package is in one of the
seven groups.

## Layout

The module root is the `dope/` directory inside the dopesuite monorepo. That is
where `go.mod` lives, with `module "dope"` in it; it is NOT at the root of the
repo. The server tree sits under a second, inner `dope/` directory, so packages
resolve as `dope/dope/<group>/<pkg>`.

```
dope/                       module root (go.mod: module "dope")
  dope/                     server tree — seven semantic groups, no loose files:
    cmd/                    entry points
      dope-server/          thin main() → dopeserver.Main(); the login bot polls in it (server/bot.go)
    server/                 package dopeserver — orchestration / the trunk
      tests/                black-box integration tests (package tests)
    web/                    HTTP / UI layer
    domain/                 game + festival domain logic
    storage/                persistence
    export/                 output generation (xlsx / json / archive)
    platform/               cross-cutting leaves (stdlib-only or near it)
```

### `server/` — the orchestration trunk (package `dopeserver`)

This is the one package that wires everything together: the HTTP mux, the
write-transaction discipline, the SSE driving, and any request handler that does
not belong in a leaf package. It imports the groups below it directly, with no
re-export shims in between. It is split into files, each of which owns one
concern:

| File | Concern |
|------|---------|
| `main.go` | entry point, mux wiring, HTTP server, SSE event handlers |
| `db.go` | DB open, active context, id resolution |
| `migrations.go` | the schema as a `[]schema.Migration` list, and the backfills it calls |
| `serve_html.go` | host/viewer/game HTML init payloads + asset versioning |
| `import_scheme.go` | the pasted-scheme importer (`/api/import`, the host form): clears the fest, calls `gamebuild.Materialise` |
| `matchview.go` | fest/match view loading + match-update application |
| `scoped_api.go` | tournament-scoped API endpoints |
| `auth.go` | sessions, auth, Telegram login bridge |
| `credentials.go` | invite/session/Telegram code + name helpers |
| `pages_public.go` | public viewer-page routing |
| `static_mode.go` | "DDoS lockdown" static-snapshot degradation layer |
| `host_accessors.go` | **dependency-inversion adapter** — see below |
| `testapi.go` | the single exported test seam for `server/tests/` |

**Dependency inversion (`host_accessors.go`).** The handler and leaf packages —
`web/pages`, `web/hostpages`, `web/telegrambridge`, `export/gameexport` and
`domain/overrides` — each declare a narrow `Host` interface describing what they
need from the server. `*server` satisfies those interfaces through thin
accessors, all collected in `host_accessors.go`, with assertions like `var _
gameexport.Host = (*server)(nil)`. This lets those packages stay leaves with no
cycles, while the server keeps its encapsulation. It is also what made it
possible to break the package up without creating import cycles.

### `web/` — HTTP / UI layer

- `pages` — public + admin page handlers (register, admin, host journal/numbers).
- `hostpages` — host editor page handlers (dashboard, roster, numbers, games).
- `editbatch` — coalesces per-game PATCH edits into one locked write tx per window.
- `telegrambridge` — the login conversation's answers, called by the in-process bot.
- `assets` — the `//go:embed static` package; the FS keeps the `static/` prefix.
  Frontend source lives under `web/ts/` (built into `static/dist/`); deno tests under `web/jstest/`.

### `domain/` — game + festival domain logic

This is where **pure** per-game and festival logic lives. Anything that needs a
database transaction or the server itself stays in `dopeserver`, and calls in
here when it needs type metadata.

- `games` is the single source of truth for everything the code knows about
  game types: the canonical codes (`EK`, `OD`, `KSI`, `SI`), the `Definition`
  registry, and the pure OD (ЧГК) domain, which covers the state shapes, parsing
  the tour composition and scoring the standings. To add a new format, register a
  `Definition`. Do not add another `switch gameType`.
- `core` — the `Engine`: shared in-memory state, write-tx plumbing, journal
  service, broadcast, revert. Embedded by `*server`.
- `structure` is the Kind registry. A Kind is one type that plays three roles:
  a `Macro`, which expands one DSL Block through the `Block` it is handed and
  declares which keys it understands; an `Expander`, which schedules a stage from
  typed config; and a `Ranker`. `schemedsl` is the DSL's parser and compiler. It
  reads the text, adapts each Block into a `structure.Block` — the столы, the
  entrants, the Protocol cascade, the reseed stages and the letters — and then
  asks the registry to expand it. There is no `switch kind` anywhere.
- `resolver` — bracket/reseed resolution. `roster` — roster + seeding.
- `gamebuild` is where a Game is created: `Create`, `Recompile`, `Rebuild`,
  `Materialise` for a pasted scheme, and `Clear`. It is the only thing that
  writes stages, matches and slots. `flatgame` treats a flat game such as ОД or
  КСИ as a Structure. Its document has two writers, `SetStateTx` and
  `PatchStateTx`, and both of them seat the бой from the team list, score it and
  rank its Block, so `stage_standings` stays up to date for flat games too.
- `overrides` — player-name overrides. `imports` — EK/seed/rating bulk import;
  a seed source is `ImportSeeds(FromKSI()|FromScheme()|FromXLSX())`, and the
  `game` source reads the source Game's one table.
- `numbering` — team-number assignment. `edit` — match-edit value types.
- `towns` — which country a town is in, as the ISO code the screen draws a flag
  from: buff answers for every town it has mirrored, and one it has not is asked
  of rating.chgk.info directly, once, at import (ADR-0020).
- `view` holds shared presentation DTOs such as `HostFest`. They are kept in a
  leaf package so that the server and the web handlers can both refer to them
  without importing each other.

### `storage/` — persistence

- `store` — SQLite schema, query helpers, and the shared view/scheme types
  (`MatchView`, `FestView`, `FestScheme`, …) plus pure scoring (`BuildView`,
  `ScoreTeam`, `ManualStandings`). Almost everything depends on it.
- `journal` — the forward-journal subsystem: on-disk codec, replay/checkpoint
  engines, hot→cold archiver, live append/read path.
- `migrate` — audit/history *data* conversion + maintenance subcommands.
- `schema` — the migration mechanism: `Apply(db, list)` runs each `Migration`
  once, in order, and records it in `schema_versions`. The list itself is the
  server's (`server/migrations.go`), since some steps call domain code.
- `festwrite` — the attribution-aware write/append facade.
- `festaccess` — per-fest access/role persistence (DB-backed authz).
- `auditmw` — audit-log write middleware. `storeutil` — scheme/query helpers.
- `sqlitez` — low-level SQLite helpers.
- `buffdb` — buff's mirror of rating.chgk.info (`DOPE_BUFF_DB`), opened
  read-only and failing soft: a missing file, table or query gives an empty
  answer rather than an error (ADR-0020).

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

- **Leaves first, and no cycles.** A package must never import `server`, which
  is `package dopeserver`. If a package needs something from the trunk, then
  either that thing moves down into the package, or it is passed in through a
  narrow `Host` interface; see `host_accessors.go`.
- **Strict downward layering.** Edges flow
  `cmd → server → web → domain/storage/platform`, and within the lower groups
  `domain → storage → platform`. Nothing in `domain/`, `storage/`, `platform/`,
  `export/` imports `web/` or `server/`. `platform/` imports no internal package
  upward of itself.
- **Registry over switches.** New game-type behaviour is registered in
  `domain/games`, not added as another `switch gameType` in a handler.
- **Refactors preserve behaviour.** The existing test suite (`just test`) and
  `just vet` are what verify that. Never let a functional change ride along with
  a refactor.

## Concurrency & outage hardening

Two locks, separated so views can never stall edits:

- `s.mu` is an RWMutex and it guards writes to the game and the database.
  Writes take `Lock`, and the few read paths that need a consistent in-memory
  view take `RLock`. Go's RWMutex gives a waiting writer priority over new
  readers, which means **edits win** when they contend with viewer reads.
- `s.subMu` guards the SSE subscriber maps, separately from `s.mu`. When a
  broadcast fans out, it takes a snapshot of the channel list under
  `subMu.RLock`, releases the lock, and only then sends. Every send is
  non-blocking, using a buffered channel that drops the oldest message. That way
  a slow or dead spectator can never block the broadcaster or an editor.

**Write-lock discipline, after the freeze on 2026-06-13.** A write must never
wait for a pooled database connection *while it is holding `s.mu`*. The pool has
only `sqliteMaxOpenConns` connections, which is 8, and it is shared with viewer
reads. If the pool is starved, a write waiting on it pins the lock indefinitely
and the whole site freezes. That is exactly what happened, for about 55 minutes.
So every write does two things:

1. acquires its connection BEFORE the lock (`acquireWriteConn`, off-lock), and
2. bounds the whole transaction with `writeTxTimeout` (5s) via
   `auditDetachedContext`, so even off-lock it can never wait forever.

Use `s.withWriteTx(reqCtx, festID, label, fn)` for this. It wraps up the whole
pattern: take the connection off-lock, then `lockWrite`, then
`beginWriteTxConn`, then commit. Only reach for the lower-level trio of
`acquireWriteConn`, `lockWrite` and `beginWriteTxConn` when a path needs to do
something between taking the lock and beginning the transaction, or after the
commit but still under the lock — updating the in-memory pointer to the active
game, for instance. Every write path that runs constantly already follows this:
game-state edits, which are batched through `web/editbatch`; match edits; the
game-state PUT; journal revert and undo; player overrides; fest numbering;
creating and deleting a game; and roster, reseed and rating import. The periodic
journal archiver bounds its background pass in the same way.

**Reaping dead connections.** Every SSE write, on `/events` and `/host-events`,
has a deadline of its own, set with `http.ResponseController` and
`sseWriteTimeout`. A client that has died or stopped consuming therefore errors
and is removed within one keepalive, instead of hanging around and inflating the
viewer count for that game.

**Write paths still on the old pattern, at lower priority.** A few paths still
take their connection while holding `s.mu`. They should be moved to
`withWriteTx`, or to the trio, whenever somebody next works on them. Leaving
them is a deliberate decision: each of them runs before the fest, or rarely, or
away from the viewer load, so there is barely any window in which the freeze
could happen. They are:
`handleHostClearGame` and `handleHostDeleteFest` (destructive, not run mid-play),
`importSchemeIntoFest` / `importSeedsFromKSI` /
`setSeedImportDeclined` (pre-fest seeding, no concurrent viewer load), and the
telegram-bridge login/register writes (tiny single statements on the bot path).
