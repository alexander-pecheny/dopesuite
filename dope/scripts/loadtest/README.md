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

- nginx is `worker_processes auto` = **1 worker**, `worker_connections 768`. Each viewer
  costs ~2 (client + upstream), so the practical ceiling is **~350 SSE viewers** before
  nginx refuses connections. 100+ is comfortable; don't expect thousands.
- nginx is already SSE-correct: `proxy_buffering off`, `proxy_read_timeout 1h`.
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

## Suggested scenario ladder

Start small, confirm clean, then climb until something bends:

1. **Baseline** `-viewers 20 -editors 1 -edit-interval 2s` — sanity; everything ~0 errors.
2. **Target** `-viewers 120 -editors 3 -edit-interval 2s` — your stated goal.
3. **Editor stress** `-viewers 120 -editors 6 -edit-interval 500ms` — push the write mutex.
4. **Viewer stress** `-viewers 300 -editors 2` — approach the nginx connection ceiling.

Find the knee, then decide whether the fix is config (nginx workers, more vCPU) or code
(per-fest locking instead of the global `s.mu`).
