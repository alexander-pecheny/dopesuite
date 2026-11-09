#!/usr/bin/env bash
# Copies buff's nightly mirror of rating.chgk.info from vps2day-ee, where buff
# updates it, to vps-he, where qy and the dope branch instances read it.
#
# The source is snapshotted with SQLite's backup API, so a copy taken while
# buff is writing is still a consistent database. The snapshot is rsynced
# onto the last one kept here, which sends only the pages that changed, and
# put in place by a rename once it passes an integrity check. A reader that
# has the old file open keeps reading it until it reconnects.
set -euo pipefail

SRC_HOST=${BUFF_SRC_HOST:-vps2day-ee}
SRC_DB=${BUFF_SRC_DB:-/home/ap/buff/buff.db}
SNAPSHOT=${BUFF_SNAPSHOT:-/tmp/buff-sync.db}
DEST=${BUFF_DEST:-/var/lib/dope-test/buff.db}
# The ssh host alias and key are pecheny's. The job runs as root, where the
# config's ~ is root's home, so the key and known hosts are named in full.
SSH_HOME=${BUFF_SSH_HOME:-/home/pecheny/.ssh}
SSH="ssh -F $SSH_HOME/config -i $SSH_HOME/id_ed25519_vps2day -o BatchMode=yes -o UserKnownHostsFile=$SSH_HOME/known_hosts"

work="$(dirname "$DEST")/.buff-sync.db"

$SSH "$SRC_HOST" python3 - "$SRC_DB" "$SNAPSHOT" <<'PY'
import os, sqlite3, sys
src, out = sys.argv[1], sys.argv[2]
tmp = out + ".tmp"
if os.path.exists(tmp):
    os.remove(tmp)
s = sqlite3.connect(f"file:{src}?mode=ro", uri=True)
d = sqlite3.connect(tmp)
s.backup(d)
d.close()
s.close()
os.replace(tmp, out)
PY

rsync -e "$SSH" --inplace --no-whole-file --times "$SRC_HOST:$SNAPSHOT" "$work"
$SSH "$SRC_HOST" rm -f "$SNAPSHOT"

if [[ "$(sqlite3 "file:$work?mode=ro" 'pragma quick_check')" != ok ]]; then
  echo "buff-sync: the copy failed its integrity check; $DEST left as it was" >&2
  exit 1
fi
players=$(sqlite3 "file:$work?mode=ro" 'select count(*) from players')
if ((players == 0)); then
  echo "buff-sync: the copy has no players; $DEST left as it was" >&2
  exit 1
fi

cp --sparse=always "$work" "$DEST.new"
chmod 0644 "$DEST.new"
mv -f "$DEST.new" "$DEST"
echo "buff-sync: $DEST updated, $players players"
