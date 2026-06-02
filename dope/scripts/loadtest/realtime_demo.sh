#!/usr/bin/env bash
# One-shot realtime demo: snapshot the public `test` fest + mint a temp editor,
# drive live OD/KSI/EK edits (with an optional ramping fleet of SSE viewers),
# then restore the fest exactly on exit (even on Ctrl-C).
#
# Two targets:
#   prod  (default) — operate on the VPS over SSH, edit https://dope.pecheny.me
#   local (LOCAL=1) — spin up a dedicated single-CPU server on a throwaway copy
#                     of tournament.db and drive it on http://localhost:PORT.
#                     This reproduces the 1-CPU VPS contention behaviour.
#
# Usage:
#   scripts/loadtest/realtime_demo.sh
#   VIEWERS=30-100 EPS=5 DURATION=600 scripts/loadtest/realtime_demo.sh
#   LOCAL=1 VIEWERS=30-100 scripts/loadtest/realtime_demo.sh
#
# Env vars (defaults):
#   LOCAL       0                        1 = local dedicated server instead of prod/SSH
#   HOST        vps2day-ee               ssh host                       (prod)
#   REMOTE_DB   /var/lib/dope/fest.db    live sqlite db on the VPS      (prod)
#   SRC_DB      tournament.db            db copied for the local server (local)
#   PORT        9690                     local server port              (local)
#   GOMAXPROCS  1                        CPUs for the local server      (local)
#   BASE        (derived)                public base URL
#   FEST        3                        fest id (the public `test` fest)
#   DURATION    180                      seconds to run
#   EPS         3                        edits per second (rotates od/ksi/ek)
#   BURST       8                        cell changes per tick
#   VIEWERS     0                        concurrent SSE viewers: "N" fixed, or
#                                        "MIN-MAX" to continually ramp (e.g. 30-100)
#   RAMP_PERIOD 60                       seconds for one min->max->min viewer cycle
#
# On exit (even Ctrl-C) the fest is restored, the demo's audit_log rows are
# purged, the DB is VACUUMed, and (local) the dedicated server is stopped.
set -euo pipefail

LOCAL=${LOCAL:-0}
HOST=${HOST:-vps2day-ee}
REMOTE_DB=${REMOTE_DB:-/var/lib/dope/fest.db}
FEST=${FEST:-3}
DURATION=${DURATION:-180}
EPS=${EPS:-3}
BURST=${BURST:-8}
VIEWERS=${VIEWERS:-0}
RAMP_PERIOD=${RAMP_PERIOD:-60}

# VIEWERS accepts "N" (fixed) or "MIN-MAX" (continually ramping).
if [[ "$VIEWERS" == *-* ]]; then
  VIEWERS_MIN=${VIEWERS%%-*}
  VIEWERS_MAX=${VIEWERS##*-}
else
  VIEWERS_MIN=$VIEWERS
  VIEWERS_MAX=$VIEWERS
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PY="$ROOT/scripts/loadtest/realtime_demo.py"
SSH=(ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST")

jq_get() { python3 -c "import json,sys; print(json.load(sys.stdin)['$1'])"; }

SRV_PID=""; TMP_DB=""; TMP_BIN=""

# prov_setup / prov_teardown abstract over prod (SSH, remote DB) vs local
# (dedicated server, throwaway DB) so the rest of the script is identical.
if [[ "$LOCAL" == 1 ]]; then
  SRC_DB=${SRC_DB:-tournament.db}
  PORT=${PORT:-9690}
  BASE=${BASE:-http://localhost:$PORT}
  DB_PATH=""  # set in prov_setup

  start_local_server() {
    TMP_DB="$(mktemp -t dope_demo_db.XXXXXX)"
    cp "$ROOT/$SRC_DB" "$TMP_DB"
    DB_PATH="$TMP_DB"
    TMP_BIN="$(mktemp -t dope_demo_bin.XXXXXX)"
    echo "==> building dedicated server -> $TMP_BIN" >&2
    ( cd "$ROOT" && go build -o "$TMP_BIN" ./dope )
    echo "==> starting server on :$PORT (GOMAXPROCS=${GOMAXPROCS:-1}) db=$TMP_DB" >&2
    ( cd "$ROOT" && DOPE_DB="$TMP_DB" PORT="$PORT" GOMAXPROCS="${GOMAXPROCS:-1}" "$TMP_BIN" ) &
    SRV_PID=$!
    for _ in $(seq 1 60); do
      if curl -fsS -o /dev/null "$BASE/" 2>/dev/null; then
        echo "==> server up" >&2; return 0
      fi
      kill -0 "$SRV_PID" 2>/dev/null || { echo "!! server died on startup" >&2; exit 1; }
      sleep 0.5
    done
    echo "!! server did not become healthy on $BASE" >&2; exit 1
  }

  prov_setup() { uv run python "$PY" setup --db "$DB_PATH" --fest "$FEST"; }
  prov_teardown() { uv run python "$PY" teardown --db "$DB_PATH" --stamp "$1"; }
  start_local_server
else
  BASE=${BASE:-https://dope.pecheny.me}
  prov_setup() { "${SSH[@]}" "python3 - setup --db '$REMOTE_DB' --fest '$FEST'" < "$PY"; }
  prov_teardown() { "${SSH[@]}" "python3 - teardown --db '$REMOTE_DB' --stamp '$1'" < "$PY"; }
fi

echo "==> setup (fest $FEST, target $BASE)" >&2
PROV=$(prov_setup)
echo "$PROV" | python3 -m json.tool >&2

STAMP=$(echo "$PROV"   | jq_get stamp)
OD=$(echo "$PROV"      | jq_get od_game)
KSI=$(echo "$PROV"     | jq_get ksi_game)
EK=$(echo "$PROV"      | jq_get ek_game)
EK_MATCH=$(echo "$PROV"| jq_get ek_match)
TOKEN=$(echo "$PROV"   | jq_get token)

cleanup() {
  echo "" >&2
  echo "==> teardown (stamp $STAMP) — restoring fest, purging demo audit rows, vacuuming" >&2
  prov_teardown "$STAMP" >&2 || \
    echo "!! teardown failed (stamp $STAMP) — restore manually with: $PY teardown --stamp $STAMP" >&2
  if [[ -n "$SRV_PID" ]]; then
    echo "==> stopping local server (pid $SRV_PID)" >&2
    kill "$SRV_PID" 2>/dev/null || true
    wait "$SRV_PID" 2>/dev/null || true
  fi
  [[ -n "$TMP_DB"  ]] && rm -f "$TMP_DB" "$TMP_DB"-wal "$TMP_DB"-shm
  [[ -n "$TMP_BIN" ]] && rm -f "$TMP_BIN"
}
trap cleanup EXIT INT TERM

cat >&2 <<EOF

  Watch these (hard-refresh once to clear cached JS):
    OD:  $BASE/fest/test/game/od/      (Итог standings re-sort live)
    KSI: $BASE/fest/test/game/ksi/
    EK:  $BASE/fest/test/game/ek/      (stage «1/16 финала, заход 1» / «Бой A»)

EOF

echo "==> driving ${EPS} edits/s for ${DURATION}s (burst ${BURST}, viewers ${VIEWERS_MIN}-${VIEWERS_MAX})" >&2
uv run python "$PY" simulate \
  --base "$BASE" --fest "$FEST" \
  --od "$OD" --ksi "$KSI" --ek "$EK" --ek-match "$EK_MATCH" \
  --token "$TOKEN" --duration "$DURATION" --eps "$EPS" --burst "$BURST" \
  --viewers-min "$VIEWERS_MIN" --viewers-max "$VIEWERS_MAX" --ramp-period "$RAMP_PERIOD"
