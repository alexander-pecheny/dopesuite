# Codebase Map

## What This Is
Tournament/championship management system with real-time web UI and Telegram bot. Handles EK (эрудит-квартет), OD/KVRM (командная викторина с раундами по минуте), and KSI (командная своя игра) formats. Russian-language domain.

## Stack
- **Backend**: Go 1.25, SQLite 3 (WAL mode, modernc.org/sqlite)
- **Frontend**: Vanilla JS + HTML/CSS, no framework, embedded in binary
- **Frontend tests**: Deno (`dope/web/jstest/`)
- **Build/run**: `justfile` (see commands below)
- **Deploy**: `just deploy`, which calls `deploy.py` (SSH-based)
- **Production** is at `ssh vps2day-ee`, use it to run commands on production server

## Note on git

This project uses `gitbutler` for managing git. Use `gitbutler` skill to familiarize yourself with this tool. For merging a branch when told so by the user, use `~/scripts/but-quick-merge.py --pull`, like that: `python ~/scripts/but-quick-merge.py --pull ui`, where `ui` is the short `gitbutler` ID of the branch.

## Directory Structure

The Go code is organised into **seven semantic groups** under the inner `dope/`
directory (no loose `.go` files at its top level). See `ARCHITECTURE.md` for the
full package-by-package breakdown and the layering rules.

```
dope/                    # module root (go.mod: module "dope")
  dope/                  # server tree — 7 groups, packages resolve as dope/dope/<group>/<pkg>
    cmd/                 # entry points: dope-server (thin main), telegram-bot
    server/              # package dopeserver — the orchestration trunk + server/tests/ (integration)
    web/                 # HTTP/UI: pages, hostpages, editbatch, telegrambridge, assets (embed), jstest
    domain/              # game/fest logic: games, core, resolver, roster, overrides, imports, numbering, edit, view
    storage/             # persistence: store, journal, migrate, festwrite, festaccess, auditmw, storeutil, sqlitez
    export/              # output: xlsxexport, gameexport
    platform/            # cross-cutting leaves: realtime, roles, markdown, session, metrics, util
scripts/
  loadtest/              # Real SSE load testing suite
  ek_restore/            # DB restoration tools
justfile                 # All task runner commands
deploy.py                # SSH deployment
.env.example             # Config template
```

## Key Files

The orchestration package `server/` (package `dopeserver`) is the trunk; it wires
the mux, the write-tx discipline and SSE, and imports the groups directly. Its
files, each one concern:

| File | Lines | Purpose |
|------|-------|---------|
| `server/main.go` | ~1000 | Entry point, mux wiring, HTTP server, SSE event handlers |
| `server/scoped_api.go` | ~1200 | Tournament-scoped API endpoints |
| `server/db.go` | ~1180 | DB bootstrap, schema migration/backfill, id resolution |
| `server/auth.go` | ~930 | Sessions, auth, Telegram login bridge |
| `server/matchview.go` | ~815 | Fest/match view loading + match-update application |
| `server/import_scheme.go` | ~480 | Fest-scheme import handlers |
| `server/static_mode.go` | ~425 | "DDoS lockdown" static-snapshot degradation layer |
| `server/serve_html.go` | ~365 | Host/viewer/game HTML init payloads + asset versioning |
| `server/host_accessors.go` | ~190 | Dependency-inversion adapter (`*server` → leaf `Host` interfaces) |
| `server/testapi.go` | ~185 | The single exported test seam for `server/tests/` |

Heavy domain/persistence logic lives in the leaf groups: `storage/store` (schema,
queries, view/scheme types, pure scoring), `storage/journal` (forward journal),
`domain/imports` (bulk roster/rating import), `domain/core` (the `Engine`),
`export/*` (xlsx/json export). Audit-log undo/redo lives in `domain/core` +
`storage/auditmw`/`migrate`.

### Frontend (`dope/web/assets/static/`)

| File | Lines | Purpose |
|------|-------|---------|
| `styles.css` | 5531 | Design system: CSS vars for all colors/spacing/typography, layout grids, table styles, theme overrides (light/dark/high-contrast) |
| `host.js` | 3153 | EK host editor — match score editing, undo/redo, stage tabs, SSE sync. Depends on `match-table.js` + `stage-cache.js` |
| `od.js` | 3012 | OD/KVRM host/viewer — tabbed results/input sheets, entry cell navigation, SSE sync. Depends on `match-table.js` |
| `match-table.js` | 2839 | **Core shared library** (`window.DopeTable`) — table builders, cell helpers, SSE parsing, state sync, floating popovers, virtual keypads, overflow controller. Used by all game pages |
| `si.js` | 1464 | KSI (team jeopardy) page — question/answer tables, team/player rows, detailed/results/refusals tabs. Depends on `match-table.js` |
| `viewer.js` | 1285 | Read-only spectator view — stages/venues/stats, floating popovers. Depends on `match-table.js` + `stage-cache.js` |
| `fest-grid.js` | 489 | Festival grid visualization — renders multiple stages horizontally, reseed panels, truncated team names |
| `menu.js` | 335 | Site-wide chrome (`window.dopeMenu`) — theme/contrast toggle, hamburger menu, account links. Loaded on every page |
| `stage-cache.js` | 289 | Shared pane cache (`window.DopeStageCache`) for EK — per-stage match state, deduped prefetch, SSE routing. Used by `host.js` + `viewer.js` |
| `login.js` | 170 | Multi-step auth UI — username → password/code branch, redirect on success |
| `profile.js` | 49 | Password change form (new password vs change password modes) |

**No module system**: files communicate via `window` globals (`DopeTable`, `DopeStageCache`, `dopeMenu`) and DOM events. Dependency order in page templates matters.

## How to Run / Build / Test
```bash
just dev-web-only     # Server only. Usually you should run this unless you need to test changes related to bot
just dev              # Run server + bot concurrently (hot reload from disk)
just test             # Go tests + Deno JS tests
just test-js          # Frontend tests only
just fmt              # gofmt
just vet              # go vet
just pre-commit       # fmt + vet + tidy-check + test (run before committing)
just deploy           # SSH deploy to VPS
just invite [days]    # Generate invite code
```

Server listens on port **9672** by default (override with `$PORT`). Database defaults to `fest.db` (override with `$DOPE_DB`).

## Architecture Patterns

**Real-time SSE sync**: Global `server.mu` RWMutex guards state; separate `server.subMu` for SSE subscribers. Per-fest subscription maps. Delta events with epoch/seq gap detection; full snapshots on resync.

**Audit log**: All mutations go to `audit_log` table via `storage/auditmw`. Undo/redo via `domain/core/revert.go`. Old logs compressed (`storage/sqlitez/audit_compress.go`) and pruned by age/disk size; audit/history data conversion lives in `storage/migrate`.

**Auth**: Session cookies (HTTP-only), role hierarchy `system → organizer → host → viewer`, per-fest scoped API tokens, Telegram bot bridges via shared-secret endpoints.

**Assets**: Embedded by the `web/assets` package (`//go:embed static`), served by `server`. Content-hash ETags for cache-busting. Dev mode reads from `dope/web/assets/static` on disk for hot reload.

**Write pattern**: Single global write lock + SQLite WAL → serialized writes. Broadcasts go out after commit. Slow-write canary detects contention.

**Game types**: EK, OD, KSI implemented as pluggable modules with independent question/match state.

## Testing UI Changes
Use `cdp.py` on port 9222 (Chrome DevTools Protocol). If there's nothing on the port, run `/Applications/Comet.app/Contents/MacOS/Comet --remote-debugging-port=9222`

## Design System
When building a new page or UI component, you MUST use the existing design
system in `dope/web/assets/static/styles.css` — its CSS variables (colors, spacing,
typography), layout grids, table styles, and component classes — and the shared
JS building blocks (`window.DopeTable` in `match-table.js`, `window.dopeMenu` in
`menu.js`, etc.). Do not introduce bespoke one-off styles or hand-rolled widgets
when a design-system equivalent exists.

Order of preference, strictly:
1. **Reuse** an existing variable / class / component as-is.
2. If something is genuinely missing, **extend the design system** — add the new
   variable/class/component to `styles.css` (or the relevant shared JS module) so
   it is reusable — rather than inlining a local solution. New tokens follow the
   existing naming and must themselves be built from existing variables where
   possible.
3. Only as a last resort, and with a comment explaining why, add page-local
   styling — but first reconsider whether step 2 is the right call.

This keeps every page visually consistent and themable (light/dark/high-contrast
all derive from the shared variables).

## CSS Convention
All CSS values must use variables — no static values on classes

## Reuse
Always reuse existing functions and classes before creating new ones

## Deployment Config
Run `just deploy` to deploy, it already handles everything that's needed.

## Production Server
- **Access**: `ssh vps2day-ee` (login user is `ap`; host `vm46153`). Some paths need `sudo` (systemd hardening hides them).
- **Live DB**: `/var/lib/dope/fest.db` (SQLite WAL mode; `-wal`/`-shm` sidecars alongside). This is the real prod DB — *not* `/home/ap/fest.db` (stale copy).
- **Services** (systemd): `dope.service` (live match server, binary `/opt/dope/dope-server`, `WorkingDirectory=/var/lib/dope`, `PORT=8090`, `EnvironmentFile=-/etc/dope.env`, `ReadWritePaths=/var/lib/dope`) and `dope-bot.service` (Telegram bot). Inspect with `systemctl cat dope.service`; find the live DB via `sudo ls -l /proc/$(systemctl show -p MainPID --value dope.service)/fd | grep .db`.
- **Backups**: ad-hoc `*.bak` snapshots live alongside the DB in `/var/lib/dope/` plus a `/var/lib/dope/backups/` dir.
- **Consistent backup**: `sqlite3` CLI (3.45.1) is installed. The service holds the DB open, so snapshot online with `sqlite3 /var/lib/dope/fest.db ".backup '/var/lib/dope/fest.db.<label>-<ts>.bak'"` (or `VACUUM INTO`) — never a bare `cp` of the `.db` alone (would miss the WAL).
