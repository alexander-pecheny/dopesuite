#!/usr/bin/env python3
"""Build and deploy a dopesuite binary to its VPS over SSH.

One script, one target table. Each target names its module, its Go package, the
binary it installs, the systemd unit it restarts, and the host it lives on —
xy and dope are on DIFFERENT hosts, so the host is per-target, not global.

  ./deploy.py --target dope-server           # the default for `just deploy` in dope/
  ./deploy.py --target xy-server,xy-bot      # the default for `just deploy` in xy/
  ./deploy.py --target dope-bot --skip-tests
  ./deploy.py --target xy-server --dry-run   # builds, uploads nothing

Every deploy backs the old binary up on the host, restarts, waits, checks the
unit is still active, and rolls back to the backup if it isn't.
"""

from __future__ import annotations

import argparse
import os
import shlex
import subprocess
import sys
import time
from pathlib import Path


ROOT = Path(__file__).resolve().parent
DIST_DIR = ROOT / "dist" / "deploy"

SSH_OPTIONS = ["-o", "BatchMode=yes", "-o", "ConnectTimeout=10"]

# Per-target defaults; override any of them from the CLI or the env vars named
# below (each app's .env is loaded by its own justfile).
#
#   host          ssh target                        {PREFIX}_DEPLOY_HOST
#   remote_dir    install dir on the host           {PREFIX}_DEPLOY_DIR
#   service       systemd unit to restart           service_env
#   module        subdir holding the go.mod         (build + `go test` run here)
#   package       Go package, relative to module
#   binary        installed name — systemd's ExecStart points at it, do not rename
#   optional      the unit is not installed on every host; skip the restart if
#                 it is absent instead of failing (xy's bot)
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
        "optional": False,
    },
    "dope-bot": {
        "host": "vps2day-ee",
        "remote_dir": "/opt/dope",
        "service": "dope-bot.service",
        "service_env": "DOPE_DEPLOY_BOT_SERVICE",
        "module": "dope",
        "package": "./dope/cmd/telegram-bot",
        "binary": "dope-bot",
        "env_prefix": "DOPE",
        "optional": False,
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
        "optional": False,
    },
    "xy-bot": {
        "host": "vps-he",
        "remote_dir": "/opt/xy",
        "service": "xy-bot.service",
        "service_env": "XY_DEPLOY_BOT",
        "module": "xy",
        "package": "./cmd/telegram-bot",
        "binary": "telegram-bot",
        "env_prefix": "XY",
        "optional": True,
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
        self.host = args.host or env.get(f"{prefix}_DEPLOY_HOST") or spec["host"]
        self.remote_dir = args.remote_dir or env.get(f"{prefix}_DEPLOY_DIR") or spec["remote_dir"]
        self.service = unit_name(args.service or env.get(spec["service_env"]) or spec["service"])
        self.module = ROOT / spec["module"]
        self.package = args.package or spec["package"]
        self.binary = args.binary or spec["binary"]
        self.optional = spec["optional"]


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


def ssh(host: str, script: str, *, capture: bool = False) -> str:
    return run(
        ["ssh", *SSH_OPTIONS, host, "bash", "-s"],
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


def build_binary(target: Target, goarch: str) -> Path:
    DIST_DIR.mkdir(parents=True, exist_ok=True)
    output = DIST_DIR / target.binary
    env = os.environ.copy()
    env.update({"CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": goarch})
    run(
        ["go", "build", "-trimpath", "-ldflags", "-s -w", "-o", output, target.package],
        cwd=target.module,
        env=env,
    )
    return output


def upload_binary(host: str, binary: Path, remote_tmp: str) -> None:
    ssh(host, f"set -euo pipefail\nmkdir -p {remote_quote(remote_tmp)}\n")
    run(["scp", *SSH_OPTIONS, binary, f"{host}:{remote_tmp}/"], cwd=ROOT)


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
OPTIONAL={1 if target.optional else 0}

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

# The optional unit (xy's bot) is not installed on every host: install the binary,
# but leave the restart alone when there is nothing to restart.
if [ "$OPTIONAL" = 1 ] && ! systemctl list-unit-files "$SERVICE" >/dev/null 2>&1; then
  echo "Installed $REMOTE_BIN; no $SERVICE on this host, skipped the restart"
  exit 0
fi

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
    return args


def main() -> int:
    args = parse_args()
    stamp = time.strftime("%Y%m%d-%H%M%S")
    targets = [Target(name, args) for name in args.targets]

    if not args.skip_tests:
        for module in dict.fromkeys(t.module for t in targets):
            run(["go", "test", "./..."], cwd=module)

    arch_by_host: dict[str, str] = {}
    for target in targets:
        goarch = args.arch or arch_by_host.get(target.host) or detect_goarch(target.host)
        arch_by_host[target.host] = goarch

        print(
            f"Deploy target: {target.name} → {target.host}:{target.remote_dir}/{target.binary} ({goarch})",
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
