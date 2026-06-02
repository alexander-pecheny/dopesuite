#!/usr/bin/env bash
# One-shot realtime demo against prod: snapshot the public `test` fest + mint a
# temp editor on the VPS, drive live OD/KSI/EK edits from here, then restore the
# fest exactly on exit (even on Ctrl-C). Same scenario as the manual runs.
#
# Usage:
#   scripts/loadtest/realtime_demo.sh
#   DURATION=600 INTERVAL=0.25 BURST=10 scripts/loadtest/realtime_demo.sh
#
# Env vars (defaults):
#   HOST        vps2day-ee               ssh host
#   REMOTE_DB   /var/lib/dope/fest.db    live sqlite db on the VPS
#   BASE        https://dope.pecheny.me  public base URL
#   FEST        3                        fest id (the public `test` fest)
#   DURATION    180                      seconds to run
#   INTERVAL    0.3                      seconds between ticks (rotates od/ksi/ek)
#   BURST       8                        cell changes per tick
set -euo pipefail

HOST=${HOST:-vps2day-ee}
REMOTE_DB=${REMOTE_DB:-/var/lib/dope/fest.db}
BASE=${BASE:-https://dope.pecheny.me}
FEST=${FEST:-3}
DURATION=${DURATION:-180}
INTERVAL=${INTERVAL:-0.3}
BURST=${BURST:-8}

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PY="$ROOT/scripts/loadtest/realtime_demo.py"
SSH=(ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST")

jq_get() { python3 -c "import json,sys; print(json.load(sys.stdin)['$1'])"; }

echo "==> setup on $HOST:$REMOTE_DB (fest $FEST)" >&2
PROV=$("${SSH[@]}" "python3 - setup --db '$REMOTE_DB' --fest '$FEST'" < "$PY")
echo "$PROV" | python3 -m json.tool >&2

STAMP=$(echo "$PROV"   | jq_get stamp)
OD=$(echo "$PROV"      | jq_get od_game)
KSI=$(echo "$PROV"     | jq_get ksi_game)
EK=$(echo "$PROV"      | jq_get ek_game)
EK_MATCH=$(echo "$PROV"| jq_get ek_match)
TOKEN=$(echo "$PROV"   | jq_get token)

cleanup() {
  echo "" >&2
  echo "==> teardown (stamp $STAMP) — restoring the test fest" >&2
  "${SSH[@]}" "python3 - teardown --db '$REMOTE_DB' --stamp '$STAMP'" < "$PY" >&2 || \
    echo "!! teardown failed — restore manually: ssh $HOST python3 - teardown --db $REMOTE_DB --stamp $STAMP < $PY" >&2
}
trap cleanup EXIT INT TERM

cat >&2 <<EOF

  Watch these (hard-refresh once to clear cached JS):
    OD:  $BASE/fest/test/game/od/      (Итог standings re-sort live)
    KSI: $BASE/fest/test/game/ksi/
    EK:  $BASE/fest/test/game/ek/      (stage «1/16 финала, заход 1» / «Бой A»)

EOF

echo "==> driving edits for ${DURATION}s (interval ${INTERVAL}, burst ${BURST})" >&2
uv run python "$PY" simulate \
  --base "$BASE" --fest "$FEST" \
  --od "$OD" --ksi "$KSI" --ek "$EK" --ek-match "$EK_MATCH" \
  --token "$TOKEN" --duration "$DURATION" --interval "$INTERVAL" --burst "$BURST"
