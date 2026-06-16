# Codebase Map

## What This Is
Tournament/championship management system with real-time web UI and Telegram bot. Handles EK (эрудит-квартет), OD/KVRM (командная викторина с раундами по минуте), and KSI (командная своя игра) formats. Russian-language domain.

## Stack
- **Backend**: Go 1.25, SQLite 3 (WAL mode, modernc.org/sqlite)
- **Frontend**: Vanilla JS + HTML/CSS, no framework, embedded in binary
- **Frontend tests**: Deno (`dope/jstest/`)
- **Build/run**: `justfile` (see commands below)
- **Deploy**: `just deploy`, which calls `deploy.py` (SSH-based)
- **Production** is at `ssh vps2day-ee`, use it to run commands on production server

## Note on git

This project uses `gitbutler` for managing git. Use `gitbutler` skill to familiarize yourself with this tool. For merging a branch when told so by the user, use `~/scripts/but-quick-merge.py --pull`, like that: `python ~/scripts/but-quick-merge.py --pull ui`, where `ui` is the short `gitbutler` ID of the branch.

## Directory Structure
```
dope/                    # Main Go package (~30K lines, 70 files)
  cmd/telegram-bot/      # Stateless Telegram bot (bridges to server via shared secret)
  static/                # Embedded frontend assets
  jstest/                # Deno JS unit tests
scripts/
  loadtest/              # Real SSE load testing suite
  ek_restore/            # DB restoration tools
justfile                 # All task runner commands
deploy.py                # SSH deployment
.env.example             # Config template
```

## Key Files
| File | Lines | Purpose |
|------|-------|---------|
| `dope/main.go` | 1439 | HTTP server, routing, SSE broadcasting, entry point |
| `dope/db.go` | 4024 | Full DB schema, all SQL queries/views |
| `dope/main_test.go` | 1965 | Integration tests |
| `dope/scoped_api.go` | 1355 | Tournament-scoped API endpoints |
| `dope/auth.go` | 1077 | Sessions, auth, Telegram login bridge |
| `dope/rating_import.go` | 1211 | Bulk roster/rating import |
| `dope/pages_host_audit.go` | 1138 | Audit log UI handlers |
| `dope/audit_revert.go` | — | Undo/redo via audit log |
| `dope/slow_write.go` | — | Lock contention canary |

All other `pages_*.go` files are HTTP handlers for specific UI pages.

### Frontend (`dope/static/`)

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

**Audit log**: All mutations go to `audit_log` table. Undo/redo via `audit_revert.go`. Old logs compressed (`audit_compress.go`) and pruned by age/disk size (`audit_prune.go`).

**Auth**: Session cookies (HTTP-only), role hierarchy `system → organizer → host → viewer`, per-fest scoped API tokens, Telegram bot bridges via shared-secret endpoints.

**Assets**: Embedded with `//go:embed static/*`. Content-hash ETags for cache-busting. Dev mode reads from disk for hot reload.

**Write pattern**: Single global write lock + SQLite WAL → serialized writes. Broadcasts go out after commit. Slow-write canary detects contention.

**Game types**: EK, OD, KSI implemented as pluggable modules with independent question/match state.

## Testing UI Changes
Use `cdp.py` on port 9222 (Chrome DevTools Protocol). If there's nothing on the port, run `/Applications/Comet.app/Contents/MacOS/Comet --remote-debugging-port=9222`

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
