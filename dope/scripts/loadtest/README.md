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

`vps2day-ee`: **1 CPU, ~960 MB RAM**, nginx → Go (`:8090`) → SQLite (`/var/lib/dope/fest.db`, WAL).

- nginx is `worker_processes auto` = **1 worker**. Each viewer costs ~2 connection slots
  (client + a fresh upstream connection — there is no upstream `keepalive`, and an active
  SSE stream holds both open for its whole life).
- **Tuned 2026-06-02** for up to ~1000 viewers: `worker_connections 4096` +
  `worker_rlimit_nofile 8192` in `/etc/nginx/nginx.conf` (backup alongside it as
  `nginx.conf.bak.*`). Ceiling is now ~2000 SSE viewers (4096 slots ÷ 2); 1000 sits at
  ~50% with headroom. Applied with `nginx -t` + `systemctl reload` (no dropped conns).
  These limits are **not in the repo** — they live only on the VPS, so re-apply them if
  the box is rebuilt. Past ~2000 the next wall is the single CPU under edit fan-out.
- nginx is already SSE-correct: `proxy_buffering off`, `proxy_read_timeout 1h`.
- `worker_processes` stays 1 (only 1 CPU) — adding workers wouldn't help.
- The **single CPU** is shared by nginx + Go + SQLite. Under concurrent edits, CPU
  saturation (not file descriptors — `LimitNOFILE=524288`) breaks first.
- The global write mutex means your test edits contend with **real users' edits**.
  Run during a quiet window.

## Layout

- `main.go` — the load driver. Run it **from your machine** (not the VPS) so it goes
  through nginx + the network like a real client. Stdlib only: `go run ./scripts/loadtest`.
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
| `viewers_failed` | 0 | hitting nginx `worker_connections` or upstream accept limits |

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

EK propagation is correlated by `(scope, revision)` rather than by an in-band
marker: the broadcast is a server-computed MatchView, not the editor's payload,
so the revision both the response and the broadcast name is what joins them.
It reports as a separate `ek` block.

To prove the per-window commit cap, point every EK editor at one match and edit
far above human rate: `-ek-editors 8 -ek-matches A -edit-interval 50ms`. Writes
should stay flat near ~6/s for that game no matter how fast the editors type.

## Suggested scenario ladder

Start small, confirm clean, then climb until something bends:

1. **Baseline** `-viewers 20 -editors 1 -edit-interval 2s` — sanity; everything ~0 errors.
2. **Target** `-viewers 120 -editors 3 -edit-interval 2s` — your stated goal.
3. **Editor stress** `-viewers 120 -editors 6 -edit-interval 500ms` — push the write mutex.
4. **Viewer stress** `-viewers 1000 -editors 2 -ramp 30s` — your worst case; expect
   `viewers_failed` to stay 0 now that the nginx limits are raised (ceiling ~2000).

Find the knee, then decide whether the fix is config (nginx workers, more vCPU) or code
(per-fest locking instead of the global `s.mu`).

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

## Gotcha: SSE needs HTTP/2 (fixed 2026-06-02)

Viewer pages hold a long-lived `EventSource`. Over **HTTP/1.1** browsers cap ~6
connections per host, so ~6 open viewer tabs exhaust the pool and any *new* tab's
fetches (EK bracket → skeleton forever) or `EventSource` (OD/KSI → no live
updates) hang. This is load-independent — purely the connection count. Fix: serve
**HTTP/2** at nginx (`listen 443 ssl http2;`), which multiplexes unlimited streams
over one connection. Verify: `curl -o /dev/null -w '%{http_version}\n' https://dope.pecheny.me/` → `2`.
