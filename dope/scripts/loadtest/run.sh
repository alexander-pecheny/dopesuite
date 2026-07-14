#!/usr/bin/env bash
# Full load-test cycle: provision a disposable fest on the VPS, drive load from
# this machine, then tear the fest down — even if the run fails or is Ctrl-C'd.
#
# Usage:
#   scripts/loadtest/run.sh
#   VIEWERS=300 EDITORS=6 EDIT_INTERVAL=500ms DURATION=120s scripts/loadtest/run.sh
#
# Env vars (with defaults):
#   HOST          vps2day-ee        ssh host
#   REMOTE_DB     /var/lib/dope/fest.db
#   BASE          https://dope.pecheny.me
#   VIEWERS       120
#   EDITORS       3
#   EDIT_INTERVAL 2s
#   DURATION      90s
#   RAMP          5s
set -euo pipefail

HOST=${HOST:-vps2day-ee}
REMOTE_DB=${REMOTE_DB:-/var/lib/dope/fest.db}
BASE=${BASE:-https://dope.pecheny.me}
VIEWERS=${VIEWERS:-120}
EDITORS=${EDITORS:-3}
EDIT_INTERVAL=${EDIT_INTERVAL:-2s}
DURATION=${DURATION:-90s}
RAMP=${RAMP:-5s}

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT/scripts/loadtest/provision.py"

echo "==> provisioning $EDITORS editors on $HOST:$REMOTE_DB" >&2
PROV_JSON=$(ssh -o BatchMode=yes "$HOST" "python3 - provision --db '$REMOTE_DB' --editors '$EDITORS'" < "$SCRIPT")
echo "$PROV_JSON" | python3 -m json.tool >&2

STAMP=$(echo "$PROV_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["stamp"])')
FEST_SLUG=$(echo "$PROV_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["fest_slug"])')
FEST_ID=$(echo "$PROV_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["fest_id"])')
GAME_ID=$(echo "$PROV_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["game_id"])')
TOKENS=$(echo "$PROV_JSON" | python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)["tokens"]))')

cleanup() {
  echo "==> tearing down (stamp $STAMP)" >&2
  ssh -o BatchMode=yes "$HOST" "python3 - teardown --db '$REMOTE_DB' --stamp '$STAMP'" < "$SCRIPT" >&2 || true
}
trap cleanup EXIT

echo "==> driving load: $VIEWERS viewers, $EDITORS editors, $DURATION" >&2
go run "$ROOT/scripts/loadtest" \
  -base "$BASE" \
  -fest "$FEST_SLUG" -fest-id "$FEST_ID" -game "$GAME_ID" \
  -viewers "$VIEWERS" -editors "$EDITORS" \
  -edit-interval "$EDIT_INTERVAL" -duration "$DURATION" -ramp "$RAMP" \
  -tokens "$TOKENS" \
  -out "$ROOT/loadtest-report-$STAMP.json"
