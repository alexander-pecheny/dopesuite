#!/usr/bin/env python3
"""Build and deploy a dopesuite binary to its VPS over SSH.

One script, one target table. Each target names its module, its Go package, the
binary it installs, the systemd unit it restarts, and the host it lives on —
xy and spliff live on one host and dope on another, so the host is per-target,
not global.

Each app is one binary: the login bot polls inside the server process, so there
is no bot unit to deploy alongside it.

  ./deploy.py --target dope-server           # the default for `just deploy` in dope/
  ./deploy.py --target xy-server             # the default for `just deploy` in xy/
  ./deploy.py --target spliff-server         # the default for `just deploy` in spliff/
  ./deploy.py --target dopetest              # dope staging (`just deploy-staging`)
  ./deploy.py --target dope-server --skip-tests
  ./deploy.py --target xy-server --dry-run   # builds, uploads nothing

Every deploy backs the old binary up on the host, restarts, waits, checks the
unit is still active, and rolls back to the backup if it isn't. When the target
host is the machine the script runs on, every step runs locally — no ssh.

The `branch` target is different: it is not one deployment but a family of
them, one per branch you want to look at, all on vps-he behind
*.dopetest.pecheny.me. Each instance is a systemd unit of the dope-test@
template, its own database, its own port and its own Caddy route, and the
first deploy provisions all four:

  ./deploy.py --target branch --instance hamsa            # -> hamsa.dopetest.pecheny.me
  ./deploy.py --target branch --instance hamsa --seed prod
  ./deploy.py --target branch --list
  ./deploy.py --target branch --instance hamsa --remove

An instance never shares anything with production: a different host, a
database of its own, and no Telegram token, so its bot cannot poll the one
prod owns.
"""

from __future__ import annotations

import argparse
import functools
import os
import re
import shlex
import shutil
import socket
import subprocess
import sys
import time
from pathlib import Path


ROOT = Path(__file__).resolve().parent
DIST_DIR = ROOT / "dist" / "deploy"

SSH_OPTIONS = ["-o", "BatchMode=yes", "-o", "ConnectTimeout=10"]

# Where a per-branch staging instance keeps its four pieces, and the band its
# port comes from. 9673-9684 are taken by xy, spliff and the review tools, and
# 978x is the verify skill's band for throwaway servers, so this starts above
# both. The zone is wildcard-routed (*.dopetest.pecheny.me) with one Caddy
# site block and one DNS-01 certificate, so a new instance needs no DNS work.
BRANCH_ROOT_DIR = "/opt/dope-test"
BRANCH_STATE_DIR = "/var/lib/dope-test"
BRANCH_ENV_DIR = "/etc/dope-test"
BRANCH_CADDY_DIR = "/etc/caddy/dopetest.d"
BRANCH_ZONE = "dopetest.pecheny.me"
BRANCH_USER = "dopetest"
BRANCH_PORT_BASE = 9791
BRANCH_PORT_LIMIT = 9819
# The prod database a --seed prod instance starts from.
BRANCH_PROD_HOST = "vps2day-ee"
BRANCH_PROD_DB = "/var/lib/dope/fest.db"

# An instance name becomes a hostname label, a systemd instance and a path, so
# it is held to what all three accept.
BRANCH_NAME_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$")

# Per-target defaults; override any of them from the CLI or the env vars named
# below (each app's .env is loaded by its own justfile).
#
#   host          ssh target                        {PREFIX}_DEPLOY_HOST
#   remote_dir    install dir on the host           {PREFIX}_DEPLOY_DIR
#   service       systemd unit to restart           service_env
#   module        subdir holding the go.mod         (build + `go test` run here)
#   package       Go package, relative to module
#   binary        installed name — systemd's ExecStart points at it, do not rename
TARGETS: dict[str, dict] = {
    "dope-server": {
        "host": "vps2day-ee",
        "remote_dir": "/opt/dope",
        "service": "dope.service",
        "service_env": "DOPE_DEPLOY_SERVICE",
        "module": "dope",
        "package": "./dope/cmd/dope-server",
        "binary": "dope-server",
        "env_prefix": "DOPE",
    },
    # Staging: the same dope binary, on the same box as prod, against a copy of
    # prod's DB (/var/lib/dopetest). It exists so a release's startup migrations
    # rehearse under the real 960MB/1-CPU limit before prod ever sees them — a
    # migration once OOM-killed prod. Deploy here first whenever a release
    # touches the schema, a backfill, or memory on the read paths.
    "dopetest": {
        "host": "vps2day-ee",
        "remote_dir": "/opt/dopetest",
        "service": "dopetest.service",
        "service_env": "DOPETEST_DEPLOY_SERVICE",
        "module": "dope",
        "package": "./dope/cmd/dope-server",
        "binary": "dope-server",
        "env_prefix": "DOPETEST",
    },
    "xy-server": {
        "host": "vps-he",
        "remote_dir": "/opt/xy",
        "service": "xy.service",
        "service_env": "XY_DEPLOY_SERVICE",
        "module": "xy",
        "package": "./cmd/xy-server",
        "binary": "xy-server",
        "env_prefix": "XY",
    },
    # Staging: the same xy binary, on the same box as prod, against a copy of
    # prod's DB (/var/lib/xytest) with prod's blobs hardlinked in. Deploy here
    # first whenever a release touches the schema or a migration. No telegram
    # bot and no litestream — bootstrap accounts with `xy-server adduser`.
    "xytest": {
        "host": "vps-he",
        "remote_dir": "/opt/xytest",
        "service": "xytest.service",
        "service_env": "XYTEST_DEPLOY_SERVICE",
        "module": "xy",
        "package": "./cmd/xy-server",
        "binary": "xy-server",
        "env_prefix": "XYTEST",
    },
    "spliff-server": {
        "host": "vps-he",
        "remote_dir": "/opt/spliff",
        "service": "spliff.service",
        "service_env": "SPLIFF_DEPLOY_SERVICE",
        "module": "spliff",
        "package": "./spliff/cmd/spliff-server",
        "binary": "spliff-server",
        "env_prefix": "SPLIFF",
    },
    # Staging: the same spliff binary, on the same box as prod, against a copy
    # of prod's DB (/var/lib/splifftest) with prod's photo blobs hardlinked in.
    # Deploy here first whenever a release touches the schema or a migration.
    # No telegram bot and no litestream — bootstrap accounts with
    # `spliff-server adduser`.
    "splifftest": {
        "host": "vps-he",
        "remote_dir": "/opt/splifftest",
        "service": "splifftest.service",
        "service_env": "SPLIFFTEST_DEPLOY_SERVICE",
        "module": "spliff",
        "package": "./spliff/cmd/spliff-server",
        "binary": "spliff-server",
        "env_prefix": "SPLIFFTEST",
    },
    # Per-branch staging. --instance names the branch and fills in the rest:
    # the unit is dope-test@<name>, the dir /opt/dope-test/<name>, and the URL
    # https://<name>.dopetest.pecheny.me. Lives on vps-he, which has the room
    # for a dozen of them; dope's prod box has 960MB and one CPU.
    "branch": {
        "host": "vps-he",
        "remote_dir": BRANCH_ROOT_DIR,
        "service": "dope-test@.service",
        "service_env": "DOPEBRANCH_DEPLOY_SERVICE",
        "module": "dope",
        "package": "./dope/cmd/dope-server",
        "binary": "dope-server",
        "env_prefix": "DOPEBRANCH",
    },
}


def unit_name(value: str) -> str:
    return value if "." in value else f"{value}.service"


class Target:
    def __init__(self, name: str, args: argparse.Namespace):
        spec = TARGETS[name]
        prefix = spec["env_prefix"]
        env = os.environ
        self.name = name
        self.instance = getattr(args, "instance", None) if name == "branch" else None
        self.host = args.host or env.get(f"{prefix}_DEPLOY_HOST") or spec["host"]
        self.remote_dir = args.remote_dir or env.get(f"{prefix}_DEPLOY_DIR") or spec["remote_dir"]
        self.service = unit_name(args.service or env.get(spec["service_env"]) or spec["service"])
        self.module = ROOT / spec["module"]
        self.package = args.package or spec["package"]
        self.binary = args.binary or spec["binary"]
        # A branch instance fills in the two fields the table left as a family:
        # its own directory under the root, and its own unit of the template.
        if self.instance:
            self.remote_dir = f"{self.remote_dir.rstrip('/')}/{self.instance}"
            self.service = f"dope-test@{self.instance}.service"

    @property
    def url(self) -> str:
        return f"https://{self.instance}.{BRANCH_ZONE}" if self.instance else ""


# ---------------------------------------------------------------- per-branch
#
# Everything below provisions one branch instance on the box. It is all
# idempotent: the shared pieces (user, template unit, Caddy include) are
# created once and then re-asserted on every deploy, so a box that lost one of
# them repairs itself, and the first instance needs no manual setup at all.

# The systemd template every instance is an instance of. %i is the branch name.
# No Telegram token is passed on: the bot that polls belongs to production, and
# two pollers on one token fight over getUpdates.
BRANCH_UNIT = f"""[Unit]
Description=dope-test@%i \u2014 dope staging for branch %i (%i.{BRANCH_ZONE})
After=network.target

[Service]
Type=simple
User={BRANCH_USER}
Group={BRANCH_USER}
WorkingDirectory={BRANCH_STATE_DIR}/%i
EnvironmentFile={BRANCH_ENV_DIR}/%i.env
ExecStart={BRANCH_ROOT_DIR}/%i/dope-server
Restart=on-failure
RestartSec=2

NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths={BRANCH_STATE_DIR}/%i
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6
LockPersonality=true

[Install]
WantedBy=multi-user.target
"""


# Caddy's wildcard certificate is issued over DNS-01, so its config only
# validates with the Cloudflare token in the environment — the same file the
# unit reads. Without this, every validate fails on the token and the reload
# gate would be useless.
CADDY_VALIDATE = (
    "sudo bash -c 'set -a; . /etc/caddy/cf.env; set +a; "
    "caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile'"
)

# Run on the production host to take a settled copy of its live database.
# sqlite3 the CLI is not installed there and does not need to be: the module
# python3 already carries does the same backup through the same C API.
PROD_SNAPSHOT_SCRIPT = """
set -euo pipefail
python3 - <<'PY'
import sqlite3
src = sqlite3.connect("file:{src}?mode=ro", uri=True)
dst = sqlite3.connect("{dst}")
with dst:
    src.backup(dst)
dst.close()
src.close()
PY
ls -l {dst}
"""


def branch_caddy_route(name: str, port: int) -> str:
    """One instance's route, imported into the wildcard site block.

    flush_interval -1 is not optional: every dope page is driven by SSE, and a
    buffering proxy holds the stream until the buffer fills.
    """
    return f"""@{name} host {name}.{BRANCH_ZONE}
handle @{name} {{
	reverse_proxy 127.0.0.1:{port} {{
		flush_interval -1
	}}
}}
"""


def branch_env(name: str, port: int) -> str:
    return f"""# dope staging for branch {name}. Written by deploy.py; edit and
# `sudo systemctl restart dope-test@{name}` if you need something else here.
PORT={port}
DOPE_ENV=staging
DOPE_DB={BRANCH_STATE_DIR}/{name}/fest.db
DOPE_TRUSTED_ORIGIN_HOSTS={name}.{BRANCH_ZONE}
# No TELEGRAM_BOT_TOKEN on purpose: the bot belongs to production.
"""


def branch_shared_setup(host: str) -> None:
    """Create what every instance shares, once. Safe to re-run."""
    script = f"""
set -euo pipefail
sudo -n true

id -u {BRANCH_USER} >/dev/null 2>&1 || sudo useradd --system --home-dir {BRANCH_STATE_DIR} \\
  --shell /usr/sbin/nologin {BRANCH_USER}
sudo install -d -m 0755 {BRANCH_ROOT_DIR}
sudo install -d -m 0755 -o {BRANCH_USER} -g {BRANCH_USER} {BRANCH_STATE_DIR}
sudo install -d -m 0750 -o root -g {BRANCH_USER} {BRANCH_ENV_DIR}
sudo install -d -m 0755 {BRANCH_CADDY_DIR}

sudo tee /etc/systemd/system/dope-test@.service >/dev/null <<'UNIT'
{BRANCH_UNIT}UNIT
sudo systemctl daemon-reload

# Teach the wildcard site block to read the per-instance routes. Done once;
# the marker is the import line itself.
if ! grep -q 'import dopetest.d/' /etc/caddy/Caddyfile; then
  echo "deploy.py: /etc/caddy/Caddyfile has no 'import dopetest.d/*.caddy' inside" >&2
  echo "the *.{BRANCH_ZONE} block. Add it and re-run." >&2
  exit 1
fi
"""
    ssh(host, script)


def branch_list(host: str) -> list[tuple[str, int, str]]:
    """Every provisioned instance as (name, port, systemd state).

    The whole loop runs under sudo, glob included: the env directory is
    root:dopetest 0750, so an unprivileged glob over it expands to nothing and
    would report an empty box — which is how two instances once got handed the
    same port.
    """
    script = f"""
set -euo pipefail
sudo bash -c '
shopt -s nullglob
for env in {BRANCH_ENV_DIR}/*.env; do
  name=$(basename "$env" .env)
  port=$(sed -n "s/^PORT=//p" "$env" | head -1)
  state=$(systemctl is-active "dope-test@$name.service" 2>/dev/null || true)
  echo "$name $port $state"
done
'
"""
    out = ssh(host, script, capture=True)
    rows = []
    for line in out.splitlines():
        parts = line.split()
        if len(parts) == 3 and parts[1].isdigit():
            rows.append((parts[0], int(parts[1]), parts[2]))
    return rows


def branch_allocate_port(host: str, name: str) -> int:
    """This instance's port: the one it already has, else the lowest free."""
    taken = {}
    for other, port, _ in branch_list(host):
        taken[other] = port
    if name in taken:
        return taken[name]
    used = set(taken.values())
    for port in range(BRANCH_PORT_BASE, BRANCH_PORT_LIMIT + 1):
        if port not in used:
            return port
    raise SystemExit(
        f"no free port in {BRANCH_PORT_BASE}-{BRANCH_PORT_LIMIT}; remove an instance first"
    )


def branch_provision(target: Target, port: int, seed: str) -> None:
    """Give the instance its directory, env, database, route and unit.

    The database is only ever created, never replaced: a deploy onto a live
    instance must not throw away what somebody has been testing with. Use
    --remove, or --seed with a fresh name, to start over.
    """
    name = target.instance
    db = f"{BRANCH_STATE_DIR}/{name}/fest.db"
    script = f"""
set -euo pipefail
sudo -n true

sudo install -d -m 0755 {BRANCH_ROOT_DIR}/{name}
sudo install -d -m 0750 -o {BRANCH_USER} -g {BRANCH_USER} {BRANCH_STATE_DIR}/{name}

sudo tee {BRANCH_ENV_DIR}/{name}.env >/dev/null <<'ENV'
{branch_env(name, port)}ENV
sudo chown root:{BRANCH_USER} {BRANCH_ENV_DIR}/{name}.env
sudo chmod 0640 {BRANCH_ENV_DIR}/{name}.env

sudo tee {BRANCH_CADDY_DIR}/{name}.caddy >/dev/null <<'ROUTE'
{branch_caddy_route(name, port)}ROUTE

if {CADDY_VALIDATE} >/dev/null 2>&1; then
  sudo systemctl reload caddy
else
  sudo rm -f {BRANCH_CADDY_DIR}/{name}.caddy
  echo "Caddy rejected the route for {name}; it has been removed." >&2
  {CADDY_VALIDATE} >&2 || true
  exit 1
fi

sudo systemctl enable dope-test@{name}.service >/dev/null
echo "provisioned {name} on port {port} ({db})"
"""
    ssh(target.host, script)
    branch_seed_db(target, db, seed)


def branch_seed_db(target: Target, db: str, seed: str) -> None:
    """Put a database under a new instance. Existing ones are left alone.

    The test runs under sudo. An instance's state directory is dopetest 0750,
    so an unprivileged `test -s` says "missing" about a database that is
    plainly there, and the caller would seed over data somebody is using.
    """
    name = target.instance
    exists = ssh(
        target.host,
        f"set -euo pipefail\n"
        f"sudo test -s {remote_quote(db)} && echo yes || echo no\n",
        capture=True,
    ).splitlines()[-1]
    if exists == "yes":
        print(f"{name}: database already there, left as it is", flush=True)
        return

    if seed == "blank":
        print(f"{name}: starting empty; the server creates the schema on boot", flush=True)
        return

    if seed == "prod":
        # sqlite3.Connection.backup is a proper online backup: it reads the
        # live database under its locks and writes one settled file, WAL
        # folded in. Nothing to install — python3 ships the module — and
        # nothing to copy alongside it.
        print(f"{name}: backing up the production database on {BRANCH_PROD_HOST}", flush=True)
        snapshot = f"/tmp/dope-prod-snapshot-{name}.db"
        ssh(BRANCH_PROD_HOST, PROD_SNAPSHOT_SCRIPT.format(src=BRANCH_PROD_DB, dst=snapshot))
        local_copy = DIST_DIR / f"prod-snapshot-{name}.db"
        DIST_DIR.mkdir(parents=True, exist_ok=True)
        run(["scp", *SSH_OPTIONS, f"{BRANCH_PROD_HOST}:{snapshot}", str(local_copy)], cwd=ROOT)
        ssh(BRANCH_PROD_HOST, f"set -euo pipefail\nrm -f {remote_quote(snapshot)}\n")

        staging_tmp = f"/tmp/dope-test-seed-{name}.db"
        upload_file(target.host, local_copy, staging_tmp)
        local_copy.unlink(missing_ok=True)
        ssh(
            target.host,
            f"set -euo pipefail\n"
            f"sudo install -m 0640 -o {BRANCH_USER} -g {BRANCH_USER} "
            f"{remote_quote(staging_tmp)} {remote_quote(db)}\n"
            f"rm -f {remote_quote(staging_tmp)}\n",
        )
        return

    # fixture: the same fest `just matrix` photographs, one game of every
    # format with every document filled in. Built by the binary we just made.
    print(f"{name}: seeding the fixture fest", flush=True)
    ssh(
        target.host,
        f"set -euo pipefail\n"
        f"sudo -u {BRANCH_USER} {BRANCH_ROOT_DIR}/{name}/dope-server seed-fixture "
        f"--db {remote_quote(db)} --slug fixture\n",
    )


def branch_remove(target: Target) -> None:
    name = target.instance
    script = f"""
set -euo pipefail
sudo -n true
sudo systemctl disable --now dope-test@{name}.service >/dev/null 2>&1 || true
sudo rm -f {BRANCH_CADDY_DIR}/{name}.caddy {BRANCH_ENV_DIR}/{name}.env
sudo rm -rf {BRANCH_ROOT_DIR}/{name} {BRANCH_STATE_DIR}/{name}
sudo systemctl reload caddy
echo "removed {name}: unit, route, binary and database are gone"
"""
    ssh(target.host, script)


def command_text(args: list) -> str:
    return " ".join(shlex.quote(str(arg)) for arg in args)


def run(
    args: list,
    *,
    cwd: Path,
    env: dict[str, str] | None = None,
    input_text: str | None = None,
    capture: bool = False,
) -> str:
    print(f"+ {command_text(args)}", flush=True)
    completed = subprocess.run(
        [str(arg) for arg in args],
        cwd=cwd,
        env=env,
        input=input_text,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.STDOUT if capture else None,
        check=False,
    )
    if completed.returncode != 0:
        if capture and completed.stdout:
            print(completed.stdout, end="")
        raise SystemExit(completed.returncode)
    return completed.stdout.strip() if capture and completed.stdout else ""


# A deploy run on the target box itself (xy's dev box is its prod host) has no
# key to ssh to itself with, so the host is resolved the way ssh would and
# compared against this machine's own addresses; a match runs the same scripts
# in a local shell.
@functools.cache
def is_local(host: str) -> bool:
    config = subprocess.run(["ssh", "-G", host], capture_output=True, text=True, check=False).stdout
    resolved = next((line.split(None, 1)[1] for line in config.splitlines() if line.startswith("hostname ")), host)
    own = {socket.gethostname(), "localhost", "127.0.0.1", "::1"}
    own.update(subprocess.run(["hostname", "-I"], capture_output=True, text=True, check=False).stdout.split())
    try:
        own.update(info[4][0] for info in socket.getaddrinfo(socket.gethostname(), None))
    except OSError:
        pass
    try:
        addresses = {info[4][0] for info in socket.getaddrinfo(resolved, None)}
    except OSError:
        addresses = set()
    return resolved in own or bool(addresses & own)


def ssh(host: str, script: str, *, capture: bool = False) -> str:
    return run(
        ["bash", "-s"] if is_local(host) else ["ssh", *SSH_OPTIONS, host, "bash", "-s"],
        cwd=ROOT,
        input_text=script,
        capture=capture,
    )


def remote_quote(value: str) -> str:
    return shlex.quote(value)


def detect_goarch(host: str) -> str:
    machine = ssh(host, "set -euo pipefail\nuname -m\n", capture=True).splitlines()[-1]
    arch_map = {
        "x86_64": "amd64",
        "amd64": "amd64",
        "aarch64": "arm64",
        "arm64": "arm64",
    }
    try:
        return arch_map[machine]
    except KeyError:
        raise SystemExit(f"Unsupported remote architecture: {machine}")


def describe_build() -> str:
    completed = subprocess.run(
        ["git", "describe", "--tags", "--always", "--dirty"],
        cwd=ROOT, capture_output=True, text=True, check=False,
    )
    return completed.stdout.strip() or "dev"


def build_binary(target: Target, goarch: str) -> Path:
    DIST_DIR.mkdir(parents=True, exist_ok=True)
    output = DIST_DIR / target.binary
    env = os.environ.copy()
    env.update({"CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": goarch})
    ldflags = f"-s -w -X pecheny.me/dopecore/buildinfo.stamped={describe_build()}"
    run(
        ["go", "build", "-trimpath", "-ldflags", ldflags, "-o", output, target.package],
        cwd=target.module,
        env=env,
    )
    return output


def upload_binary(host: str, binary: Path, remote_tmp: str) -> None:
    ssh(host, f"set -euo pipefail\nmkdir -p {remote_quote(remote_tmp)}\n")
    if is_local(host):
        print(f"+ cp {binary} {remote_tmp}/", flush=True)
        shutil.copy2(binary, remote_tmp)
        return
    run(["scp", *SSH_OPTIONS, binary, f"{host}:{remote_tmp}/"], cwd=ROOT)


def upload_file(host: str, local: Path, remote_path: str) -> None:
    """upload_binary for one named destination file rather than a directory."""
    if is_local(host):
        print(f"+ cp {local} {remote_path}", flush=True)
        shutil.copy2(local, remote_path)
        return
    run(["scp", *SSH_OPTIONS, str(local), f"{host}:{remote_path}"], cwd=ROOT)


def install_and_restart(target: Target, *, remote_tmp: str, stamp: str, health_wait: int) -> None:
    remote_bin = f"{target.remote_dir.rstrip('/')}/{target.binary}"
    tmp_bin = f"{remote_tmp.rstrip('/')}/{target.binary}"
    backup = f"{remote_bin}.{stamp}.bak"
    script = f"""
set -euo pipefail

REMOTE_TMP={remote_quote(remote_tmp)}
REMOTE_DIR={remote_quote(target.remote_dir)}
REMOTE_BIN={remote_quote(remote_bin)}
TMP_BIN={remote_quote(tmp_bin)}
BACKUP={remote_quote(backup)}
SERVICE={remote_quote(target.service)}
HEALTH_WAIT={health_wait}

cleanup() {{
  rm -rf "$REMOTE_TMP"
}}

rollback() {{
  if [ -e "$BACKUP" ]; then
    echo "Deploy failed; restoring $BACKUP" >&2
    sudo install -m 0755 "$BACKUP" "$REMOTE_BIN"
    sudo systemctl restart "$SERVICE" || true
  fi
}}

trap cleanup EXIT

sudo -n true
test -s "$TMP_BIN"
sudo install -d -m 0755 "$REMOTE_DIR"
if [ -e "$REMOTE_BIN" ]; then
  sudo cp -a "$REMOTE_BIN" "$BACKUP"
fi
sudo install -m 0755 "$TMP_BIN" "$REMOTE_BIN"

if ! sudo systemctl restart "$SERVICE"; then
  rollback
  exit 1
fi

sleep "$HEALTH_WAIT"
if ! sudo systemctl is-active --quiet "$SERVICE"; then
  sudo journalctl -u "$SERVICE" -n 40 --no-pager >&2 || true
  rollback
  exit 1
fi

sudo systemctl --no-pager --full status "$SERVICE" | sed -n '1,12p'
echo "Deployed $REMOTE_BIN and restarted $SERVICE"
"""
    ssh(target.host, script)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument(
        "--target",
        default="dope-server",
        help="Comma-separated targets: " + ", ".join(sorted(TARGETS)),
    )
    parser.add_argument("--host", help="Override the target's ssh host")
    parser.add_argument("--remote-dir", help="Override the target's install dir")
    parser.add_argument("--service", help="Override the systemd unit (single target only)")
    parser.add_argument("--package", help="Override the Go package (single target only)")
    parser.add_argument("--binary", help="Override the installed binary name (single target only)")
    parser.add_argument(
        "--arch",
        default=os.environ.get("DEPLOY_ARCH")
        or os.environ.get("DOPE_DEPLOY_ARCH")
        or os.environ.get("XY_DEPLOY_ARCH"),
        choices=["amd64", "arm64"],
        help="Skip the remote uname probe and cross-compile for this arch",
    )
    parser.add_argument(
        "--instance",
        help="Branch name for --target branch; becomes <name>." + BRANCH_ZONE,
    )
    parser.add_argument(
        "--seed",
        choices=["fixture", "prod", "blank"],
        default="fixture",
        help="What a NEW branch instance's database starts as (default: fixture)",
    )
    parser.add_argument("--list", action="store_true", help="List branch instances and exit")
    parser.add_argument("--remove", action="store_true", help="Remove the named branch instance and exit")
    parser.add_argument("--skip-tests", action="store_true", help="Build without running go test ./...")
    parser.add_argument("--health-wait", type=int, default=2, help="Seconds to wait before checking systemd")
    parser.add_argument("--dry-run", action="store_true", help="Build only; do not upload or restart")
    args = parser.parse_args()

    args.targets = [t.strip() for t in args.target.split(",") if t.strip()]
    if not args.targets:
        parser.error("--target is empty")
    for name in args.targets:
        if name not in TARGETS:
            parser.error(f"unknown target {name!r}; choose from {', '.join(sorted(TARGETS))}")
    if len(args.targets) > 1 and (args.service or args.package or args.binary):
        parser.error("--service/--package/--binary only make sense with a single --target")

    branch = args.targets == ["branch"]
    if args.instance and not branch:
        parser.error("--instance only makes sense with --target branch")
    if branch and len(args.targets) > 1:
        parser.error("--target branch deploys one instance at a time")
    if branch and not args.list and not args.instance:
        parser.error("--target branch needs --instance <name> (or --list)")
    if args.remove and not args.instance:
        parser.error("--remove needs --instance <name>")
    if args.instance and not BRANCH_NAME_RE.match(args.instance):
        parser.error(
            f"instance name {args.instance!r} must be lowercase letters, digits and "
            "dashes, 2-32 characters, starting and ending with a letter or digit"
        )
    return args


def run_branch(args: argparse.Namespace, stamp: str) -> int:
    """The per-branch lifecycle: list, remove, or provision-then-deploy."""
    target = Target("branch", args)

    if args.list:
        rows = branch_list(target.host)
        if not rows:
            print("no branch instances")
            return 0
        width = max(len(name) for name, _, _ in rows)
        for name, port, state in sorted(rows):
            print(f"{name:<{width}}  {port}  {state:<10}  https://{name}.{BRANCH_ZONE}")
        return 0

    if args.remove:
        branch_remove(target)
        return 0

    goarch = args.arch or detect_goarch(target.host)
    print(
        f"Branch instance: {target.instance} → {target.url} "
        f"({target.host}:{target.remote_dir}, {goarch})",
        flush=True,
    )
    binary = build_binary(target, goarch)
    if args.dry_run:
        print(f"Built {binary}; dry run requested, nothing provisioned.")
        return 0

    branch_shared_setup(target.host)
    port = branch_allocate_port(target.host, target.instance)

    # The binary goes in before the database does: --seed fixture runs
    # `dope-server seed-fixture`, which is this very binary.
    remote_tmp = f"/tmp/dope-test-{target.instance}-{stamp}"
    upload_binary(target.host, binary, remote_tmp)
    ssh(
        target.host,
        f"set -euo pipefail\n"
        f"sudo install -d -m 0755 {remote_quote(target.remote_dir)}\n"
        f"sudo install -m 0755 {remote_quote(remote_tmp)}/{target.binary} "
        f"{remote_quote(target.remote_dir)}/{target.binary}\n"
        f"rm -rf {remote_quote(remote_tmp)}\n",
    )
    branch_provision(target, port, args.seed)

    ssh(
        target.host,
        f"set -euo pipefail\n"
        f"sudo systemctl restart {remote_quote(target.service)}\n"
        f"sleep {args.health_wait}\n"
        f"if ! sudo systemctl is-active --quiet {remote_quote(target.service)}; then\n"
        f"  sudo journalctl -u {remote_quote(target.service)} -n 40 --no-pager >&2\n"
        f"  exit 1\n"
        f"fi\n"
        f"sudo systemctl --no-pager --full status {remote_quote(target.service)} | sed -n '1,8p'\n",
    )
    print(f"\n{target.instance} is up at {target.url}", flush=True)
    return 0


def main() -> int:
    args = parse_args()
    stamp = time.strftime("%Y%m%d-%H%M%S")

    if args.targets == ["branch"]:
        if not args.skip_tests and not args.list and not args.remove:
            run(["go", "test", "./..."], cwd=ROOT / TARGETS["branch"]["module"])
        return run_branch(args, stamp)

    targets = [Target(name, args) for name in args.targets]

    if not args.skip_tests:
        for module in dict.fromkeys(t.module for t in targets):
            run(["go", "test", "./..."], cwd=module)

    arch_by_host: dict[str, str] = {}
    for target in targets:
        goarch = args.arch or arch_by_host.get(target.host) or detect_goarch(target.host)
        arch_by_host[target.host] = goarch

        where = "this host" if is_local(target.host) else target.host
        print(
            f"Deploy target: {target.name} → {where}:{target.remote_dir}/{target.binary} ({goarch})",
            flush=True,
        )
        binary = build_binary(target, goarch)

        if args.dry_run:
            print(f"Built {binary}; dry run requested, skipping upload.")
            continue

        remote_tmp = f"/tmp/{target.name}-deploy-{stamp}"
        upload_binary(target.host, binary, remote_tmp)
        install_and_restart(target, remote_tmp=remote_tmp, stamp=stamp, health_wait=args.health_wait)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        print("Interrupted", file=sys.stderr)
        raise SystemExit(130)
