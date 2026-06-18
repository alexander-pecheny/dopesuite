# Architecture & package layout

This document describes how the Go code is organised and the intended direction
for further modularisation. It complements the file-by-file map in `AGENTS.md`.

## Why this exists

The server started life as a single flat `package main` (~70 files, ~32K lines).
That makes it hard to see the seams: game-specific logic, the persistence layer,
the realtime/SSE layer and the HTTP handlers all live side by side, and
game-type knowledge (`"ek"`/`"od"`/`"ksi"`/`"si"` literals and `switch gameType`
blocks) is scattered across a dozen files. With more game formats coming
(10–15 expected), that sprawl only gets worse.

The refactor introduces real package boundaries, starting with the parts that
can be extracted **safely and without behaviour change** — leaf packages that
depend only on the standard library / third-party code, never on the server,
database or HTTP layers. Leaf-first keeps the change low-risk (no import cycles,
fully compiler- and test-verified) which matters because live tournaments run on
this code.

## Current packages

```
dope/                      module root (go.mod: module "dope")
  dope/                    main server package (package main) — HTTP, DB, SSE, handlers
    games/                 package games  — game-type registry + per-game pure domain logic
    markdown/              package markdown — host-authored markdown → safe HTML
    cmd/telegram-bot/      standalone Telegram bot (bridges to the server)
    static/               embedded frontend assets
    jstest/               Deno frontend tests
```

### `games` (leaf)

The single source of truth for game-type knowledge. Generic, type-agnostic
server code consults the registry here instead of switching on raw strings:

- `games.go` — canonical type codes (`EK`, `OD`, `KSI`, `SI`), the `Default`,
  the `Definition` registry, and helpers (`Label`, `IsKnown`, `IsChGK`,
  `Lookup`). Add a new format by registering a `Definition`.
- `od.go` — pure OD (ЧГК) domain: the persisted-state shapes (`ODState`,
  `ODTeam`), tour-composition parsing (`ParseTourComp`) and standings scoring
  (`ComputeODResults`), shared by the xlsx export and the server-side results
  view so the two scoring paths can't drift.

This is the home for **pure** per-game domain logic. Logic that needs a DB
transaction or the server (game creation, roster propagation, journal rendering)
stays in `package main` for now and calls into `games` for the type metadata —
see the roadmap below.

### `markdown` (leaf)

Wraps goldmark to render host-authored markdown (fest descriptions) to safe
HTML, including the custom `:::details` disclosure block, with raw-HTML
passthrough deliberately disabled. Entry point: `markdown.Render`.

## Roadmap — further submodules

These are the natural seams for continued decomposition. They are **not** done
yet because they touch the tightly-coupled core (the `server` god-struct and the
shared types in `db.go`); doing them well means first untangling that coupling,
which is too risky to land immediately before a live fest. They are listed here
so the direction is explicit and the next change has a target to aim at.

Roughly in increasing order of effort:

1. **`games/ek`, `games/od`, `games/ksi`** — promote the remaining per-game
   domain (empty-state builders, KSI/EK state shapes, EK bracket helpers,
   xlsx sheet builders) into per-format subpackages behind a small interface, so
   adding a format is "add a package + register it" rather than editing a dozen
   shared files.
2. **`authz` (or `roles`)** — the permission model (role hierarchy, the
   `festRoleCan*` predicates, `normalizeFestRole`). The pure predicates are a
   clean leaf; the DB-backed access management splits off as a thin layer on top.
3. **`journal`** — the forward-journal / audit subsystem (the `journal_*.go`
   cluster, ~12 files). Largely self-contained conceptually but currently
   reaches into `db.go` helpers; extract once those helpers are package-scoped.
4. **`realtime` (SSE)** — subscriber registry, delta coalescing and broadcast
   fan-out, currently methods on `*server`. Extract behind a small publisher
   interface.
5. **`store`** — the SQLite schema, queries and shared view types from `db.go`.
   The biggest and last step, because almost everything depends on it; it forces
   the shared types out of `package main` and breaks the remaining cycles.
6. **`cmd/dope-server`** — once the above land, the root `package main` becomes a
   thin entry point wiring the packages together, moved under `cmd/` to match
   `cmd/telegram-bot`.

### Guiding principles

- **Leaf-first, no cycles.** A new package must not import `package main`. If it
  needs something from the core, that something moves down with it or is passed
  in as a parameter/interface.
- **Behaviour-preserving.** Refactors are verified by the existing test suite
  (`just test`) plus `just vet`; no functional change rides along.
- **Registry over switches.** New game-type behaviour is registered in `games`,
  not added as another `switch gameType` in a handler.

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
write paths all follow this: game-state edits (batched, `edit_batch.go`), match
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
`telegram_bridge` login/register writes (tiny single statements on the bot path).
