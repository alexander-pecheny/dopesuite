# dope load test

Measures how production (https://dope.pecheny.me) holds up with **many concurrent
editors while 100+ viewers watch live**. It opens real SSE viewer streams and pushes
real edits, then reports the numbers that matter for this app:

- **edit latency & error rate** — the write path. Every edit takes a process-global
  `s.mu` lock + a single SQLite writer (`busy_timeout=5s`), so concurrent editors
  serialize. This is the #1 thing to watch.
- **propagation latency** — time from an editor's send to a viewer receiving it over
  SSE. Editors and viewers share the driver's clock, so this is exact.
- **delivery ratio** — fraction of (edit × viewer) pairs delivered. The server drops
  the oldest queued event for a slow viewer (8-slot channel), so gaps surface here.

## What the production box actually is

`vps2day-ee`: **1 CPU, ~960 MB RAM**, Caddy → Go (`:8090`) → SQLite (`/var/lib/dope/fest.db`, WAL).

- The reverse proxy is **Caddy** (`/etc/caddy/Caddyfile`), which also issues the TLS
  certs and serves HTTP/2. The `/etc/nginx/` tree on the box is a dead leftover — nginx
  is not running, so its worker/connection tuning (and the notes that used to be here
  about it) no longer applies to anything.
- SSE needs `flush_interval -1` on the `reverse_proxy` block, which every dope site has.
- The **single CPU** is shared by Caddy + Go + SQLite. Under concurrent edits, CPU
  saturation (not file descriptors — `LimitNOFILE=524288`) breaks first.
- The global write mutex means your test edits contend with **real users' edits**.
  Run during a quiet window.

## Layout

- `main.go` — the load driver. Run it **from your machine** (not the VPS) so it goes
  through Caddy + the network like a real client. Stdlib only: `go run ./scripts/loadtest`.
- `provision.py` — run **on the VPS**. Seeds a disposable public fest + a game + N
  editor accounts, and injects a session token per editor (no login needed). Teardown
  deletes exactly what it created.
- `run.sh` — convenience wrapper: provision over SSH → run locally → tear down.

## Manual run

```bash
# 1. Provision on the VPS (prints JSON: fest_slug, fest_id, game_id, tokens[]).
ssh vps2day-ee 'python3 - --editors 3 < /dev/stdin' < scripts/loadtest/provision.py provision
#   …or copy provision.py over and run it there. Note the stamp it prints for teardown.

# 2. Drive load from your machine.
go run ./scripts/loadtest \
  -base https://dope.pecheny.me \
  -fest <fest_slug> -fest-id <fest_id> -game <game_id> \
  -viewers 120 -editors 3 -edit-interval 2s -duration 90s \
  -tokens <tok1>,<tok2>,<tok3> \
  -out report.json

# 3. Tear down on the VPS.
ssh vps2day-ee 'python3 /opt/dope/provision.py teardown --stamp <stamp>'
```

`run.sh` does all three for you (see top of the file for env vars).

## Watch the server while it runs

In another terminal:

```bash
ssh vps2day-ee 'pidstat -p $(systemctl show dope.service -p MainPID --value) 1'   # CPU per second
ssh vps2day-ee 'watch -n1 "ss -s; journalctl -u dope.service --since \"1 min ago\" | tail"'
```

## Reading the report

| Signal | Healthy | Trouble means |
|---|---|---|
| `edits_5xx_busy` | 0 | SQLite `busy`/write-lock timeouts — editor contention is real |
| `edit_latency_ms_p95` | low tens | climbing into seconds → serialized behind the write mutex |
| `delivery_ratio` | ~1.0 | < 1 → slow viewers' event buffers overflowing (fan-out backpressure) |
| `propagation_ms_p95` | sub-second | seconds → CPU saturated or viewers backed up |
| `viewers_failed` | 0 | hitting the proxy's connection ceiling or upstream accept limits |

## EK editors (the tournament path)

Every EK edit is a blob-path set-op `PATCH .../matches/{code}/state` (ADR-0005),
batched server-side into one write per game per 150 ms window. Add EK editors
alongside the flat ones — they share the token pool:

```bash
go run ./scripts/loadtest -base https://dopetest.pecheny.me \
  -fest <slug> -fest-id <id> -game <gid> \
  -viewers 100 -editors 3 -ek-editors 6 -ek-matches A,B,C \
  -edit-interval 2s -duration 15m -tokens <toks> -out report.json
```

EK propagation is correlated by revision rather than by an in-band marker: the
broadcast is a server-computed MatchView, not the editor's payload. Deltas
coalesce, so an arrival at revision R proves every edit up to R reached that
viewer, and the join is a merge over revision order per viewer. It reports as a
separate `ek` block.

Flat editors (`-edit-mode patch`, the default) send the same set-ops od/KSI
clients do, each stamping its own `_lt/<editor>` marker path so a merged window
still shows one delivery per co-editor. `-edit-mode put` keeps the old
whole-state write for comparison.

To prove the per-window commit cap, point every EK editor at one match and edit
far above human rate: `-ek-editors 8 -ek-matches A -edit-interval 50ms`. Writes
should stay flat near ~6/s for that game no matter how fast the editors type.

## Suggested scenario ladder

Start small, confirm clean, then climb until something bends:

1. **Baseline** `-viewers 20 -editors 1 -edit-interval 2s` — sanity; everything ~0 errors.
2. **Target** `-viewers 120 -editors 3 -edit-interval 2s` — your stated goal.
3. **Editor stress** `-viewers 120 -editors 6 -edit-interval 500ms` — push the write mutex.
4. **Viewer stress** `-viewers 1000 -editors 2 -ramp 30s` — your worst case; expect
   `viewers_failed` to stay 0.

Find the knee, then decide whether the fix is config (more vCPU) or code (per-fest
locking instead of the global `s.mu`).

## Realtime multi-game demo (`realtime_demo.sh`)

Drives watchable OD/KSI/EK edits on the public `test` fest, optionally with a
continually-ramping fleet of SSE viewers, and reports edit & view (propagation)
latency percentiles. Restores the fest + purges its own audit_log rows + VACUUMs
on exit.

```bash
# prod (over SSH), 30–100 ramping viewers, 5 edits/s, 10 min
VIEWERS=30-100 EPS=5 DURATION=600 scripts/loadtest/realtime_demo.sh

# local repro: dedicated GOMAXPROCS=1 server on a throwaway fest.db copy
LOCAL=1 VIEWERS=30-100 scripts/loadtest/realtime_demo.sh
```

## Staging: dopetest.pecheny.me

`dopetest.pecheny.me` is a second dope instance on the same box (`dopetest.service`,
`:8091`, `/var/lib/dopetest/fest.db`) running on a **copy** of the prod DB, so a release
can be loadtested — and its startup migrations rehearsed — under the real memory limit
without touching prod. Refresh its DB with the same online snapshot prod uses:

```bash
ssh vps2day-ee 'sudo systemctl stop dopetest &&
  python3 -c "import sqlite3; src=sqlite3.connect(\"/var/lib/dope/fest.db\"); dst=sqlite3.connect(\"/var/lib/dopetest/fest.db\"); src.backup(dst)" &&
  sudo systemctl start dopetest'
```

`provision.py grant --fest <id>` adds editor accounts to a fest that already exists
(the prod copy has real stages, matches and rosters, which `provision` does not seed);
`reopen --game <id> --codes A,B,C` sets finished bouts back to active so editors can
type into them. Both are undone by the same `teardown --stamp`.

## Gotcha: SSE needs HTTP/2 (fixed 2026-06-02)

Viewer pages hold a long-lived `EventSource`. Over **HTTP/1.1** browsers cap ~6
connections per host, so ~6 open viewer tabs exhaust the pool and any *new* tab's
fetches (EK bracket → skeleton forever) or `EventSource` (OD/KSI → no live
updates) hang. This is load-independent — purely the connection count. Fix: serve
**HTTP/2**, which Caddy serves by default and multiplexes unlimited streams over one
connection. Verify: `curl -o /dev/null -w '%{http_version}\n' https://dope.pecheny.me/` → `2`.
