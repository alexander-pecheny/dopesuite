# Codebase Map

## What This Is
Tournament/championship management system with real-time web UI and Telegram bot. Handles EK (эрудит-квартет), OD/KVRM (командная викторина с раундами по минуте), and KSI (командная своя игра) formats. Russian-language domain.

## Stack
- **Backend**: Go 1.25, SQLite 3 (WAL mode, modernc.org/sqlite)
- **Frontend**: Vanilla JS + HTML/CSS, no framework, embedded in binary
- **Frontend tests**: Deno (`dope/jstest/`)
- **Build/run**: `justfile` (see commands below)
- **Deploy**: `just deploy`, which calls `deploy.py` (SSH-based)

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
