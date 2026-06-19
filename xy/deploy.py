#!/usr/bin/env python3
"""Build the xy binaries and deploy them to a VPS over SSH.

Configuration (env vars, or a .env file loaded by `just`):
  XY_DEPLOY_HOST     ssh target, e.g. "vps2day-ee"            (required)
  XY_DEPLOY_DIR      remote install dir for binaries           (default /opt/xy)
  XY_DEPLOY_DATA     remote working dir (DB + blobs)           (default /var/lib/xy)
  XY_DEPLOY_SERVICE  systemd service name for the server       (default xy)
  XY_DEPLOY_BOT      systemd service name for the bot          (default xy-bot)

Cross-compiles for linux/amd64 (pure-Go SQLite → no cgo), copies the binaries,
and restarts the systemd services. Usage:

  just deploy            # build + push + restart
  just deploy --dry-run  # print the steps without running them
"""
import os
import subprocess
import sys
import tempfile

DRY = "--dry-run" in sys.argv


def run(cmd, **kw):
    print("+", " ".join(cmd))
    if DRY:
        return
    subprocess.run(cmd, check=True, **kw)


def main():
    host = os.environ.get("XY_DEPLOY_HOST")
    if not host:
        sys.exit("set XY_DEPLOY_HOST (ssh target)")
    install_dir = os.environ.get("XY_DEPLOY_DIR", "/opt/xy")
    service = os.environ.get("XY_DEPLOY_SERVICE", "xy")
    bot = os.environ.get("XY_DEPLOY_BOT", "xy-bot")

    tmp = tempfile.mkdtemp(prefix="xy-deploy-")
    env = {**os.environ, "GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0"}
    server_bin = os.path.join(tmp, "xy-server")
    bot_bin = os.path.join(tmp, "telegram-bot")

    run(["go", "build", "-o", server_bin, "./cmd/xy-server"], env=env)
    run(["go", "build", "-o", bot_bin, "./cmd/telegram-bot"], env=env)

    # Stage into a temp remote path, then move into place (atomic-ish) + restart.
    run(["ssh", host, f"mkdir -p {install_dir}"])
    run(["scp", server_bin, f"{host}:{install_dir}/xy-server.new"])
    run(["scp", bot_bin, f"{host}:{install_dir}/telegram-bot.new"])
    run(["ssh", host,
         f"sudo install -m755 {install_dir}/xy-server.new {install_dir}/xy-server && "
         f"sudo install -m755 {install_dir}/telegram-bot.new {install_dir}/telegram-bot && "
         f"sudo systemctl restart {service} && sudo systemctl restart {bot}"])
    print("deployed.")


if __name__ == "__main__":
    main()
