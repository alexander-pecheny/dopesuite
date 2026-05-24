#!/usr/bin/env python3
"""Build and deploy the Dope web server to the VPS."""

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

DEFAULT_HOST = "vps2day-ee"
DEFAULT_REMOTE_DIR = "/opt/dope"
SSH_OPTIONS = ["-o", "BatchMode=yes", "-o", "ConnectTimeout=10"]

# Per-target defaults. Override any of these from the CLI when needed.
TARGETS: dict[str, dict[str, str]] = {
    "server": {
        "service": "dope.service",
        "package": "./dope",
        "binary": "dope-server",
    },
    "bot": {
        "service": "dope-bot.service",
        "package": "./dope/cmd/telegram-bot",
        "binary": "dope-bot",
    },
}
DEFAULT_TARGET = "server"


def command_text(args: list[str | Path]) -> str:
    return " ".join(shlex.quote(str(arg)) for arg in args)


def run(
    args: list[str | Path],
    *,
    cwd: Path = ROOT,
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
    return run(["ssh", *SSH_OPTIONS, host, "bash", "-s"], input_text=script, capture=capture)


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


def build_binary(package: str, binary_name: str, goarch: str, skip_tests: bool) -> Path:
    if not skip_tests:
        run(["go", "test", "./..."])

    DIST_DIR.mkdir(parents=True, exist_ok=True)
    output = DIST_DIR / binary_name
    env = os.environ.copy()
    env.update(
        {
            "CGO_ENABLED": "0",
            "GOOS": "linux",
            "GOARCH": goarch,
        }
    )
    run(["go", "build", "-trimpath", "-ldflags", "-s -w", "-o", output, package], env=env)
    return output


def upload_binary(host: str, binary: Path, remote_tmp: str) -> None:
    ssh(host, f"set -euo pipefail\nmkdir -p {remote_quote(remote_tmp)}\n")
    run(["scp", *SSH_OPTIONS, binary, f"{host}:{remote_tmp}/"])


def install_and_restart(
    *,
    host: str,
    remote_tmp: str,
    remote_dir: str,
    service: str,
    binary_name: str,
    stamp: str,
    health_wait: int,
) -> None:
    remote_bin = f"{remote_dir.rstrip('/')}/{binary_name}"
    tmp_bin = f"{remote_tmp.rstrip('/')}/{binary_name}"
    backup = f"{remote_bin}.{stamp}.bak"
    script = f"""
set -euo pipefail

REMOTE_TMP={remote_quote(remote_tmp)}
REMOTE_DIR={remote_quote(remote_dir)}
REMOTE_BIN={remote_quote(remote_bin)}
TMP_BIN={remote_quote(tmp_bin)}
BACKUP={remote_quote(backup)}
SERVICE={remote_quote(service)}
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
    ssh(host, script)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--target",
        default=os.environ.get("DOPE_DEPLOY_TARGET", DEFAULT_TARGET),
        choices=sorted(TARGETS),
        help="Which component to deploy. Sets package/binary/service presets.",
    )
    parser.add_argument("--host", default=os.environ.get("DOPE_DEPLOY_HOST", DEFAULT_HOST))
    parser.add_argument("--remote-dir", default=os.environ.get("DOPE_DEPLOY_DIR", DEFAULT_REMOTE_DIR))
    parser.add_argument("--service", default=os.environ.get("DOPE_DEPLOY_SERVICE"))
    parser.add_argument("--package", default=os.environ.get("DOPE_DEPLOY_PACKAGE"))
    parser.add_argument("--binary", default=os.environ.get("DOPE_DEPLOY_BINARY"))
    parser.add_argument("--arch", default=os.environ.get("DOPE_DEPLOY_ARCH"), choices=["amd64", "arm64"])
    parser.add_argument("--skip-tests", action="store_true", help="Build without running go test ./...")
    parser.add_argument("--health-wait", type=int, default=2, help="Seconds to wait before checking systemd")
    parser.add_argument("--dry-run", action="store_true", help="Build only; do not upload or restart")
    args = parser.parse_args()
    preset = TARGETS[args.target]
    if args.service is None:
        args.service = preset["service"]
    if args.package is None:
        args.package = preset["package"]
    if args.binary is None:
        args.binary = preset["binary"]
    return args


def main() -> int:
    args = parse_args()
    stamp = time.strftime("%Y%m%d-%H%M%S")
    goarch = args.arch or detect_goarch(args.host)

    print(f"Deploy target: {args.target} → {args.host}:{args.remote_dir}/{args.binary} ({goarch})", flush=True)
    binary = build_binary(args.package, args.binary, goarch, args.skip_tests)

    if args.dry_run:
        print(f"Built {binary}; dry run requested, skipping upload.")
        return 0

    remote_tmp = f"/tmp/dope-deploy-{stamp}"
    upload_binary(args.host, binary, remote_tmp)
    install_and_restart(
        host=args.host,
        remote_tmp=remote_tmp,
        remote_dir=args.remote_dir,
        service=args.service,
        binary_name=args.binary,
        stamp=stamp,
        health_wait=args.health_wait,
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        print("Interrupted", file=sys.stderr)
        raise SystemExit(130)
