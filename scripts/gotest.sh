#!/usr/bin/env bash
# go test with a summary: when it fails, the last lines name every failing
# test, so a long log (the replays' slow-write lines, a tail in a script)
# never hides which one it was. The whole log stays in $TMPDIR.
set -uo pipefail
log=$(mktemp "${TMPDIR:-/tmp}/gotest.XXXXXX")
go test "$@" 2>&1 | tee "$log"
rc=${PIPESTATUS[0]}
if ((rc != 0)); then
    echo
    echo "failed tests (full log: $log):"
    grep -E '^\s*--- FAIL|^FAIL\s|^panic:' "$log" || echo "  none named: the package failed to build or timed out"
else
    rm -f "$log"
fi
exit $rc
