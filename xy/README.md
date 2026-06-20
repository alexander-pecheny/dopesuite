# xy

A Trello-style board app for ЧГК (trivia) question editing. Every piece of
user-entered data (board/list/card/label/comment/attachment) is **encrypted
client-side** with a per-board passphrase; the server only stores and serves
ciphertext plus the structural metadata needed to order, sync, and authorize.

- **Backend**: Go 1.26, SQLite (WAL, `modernc.org/sqlite`, pure Go, no cgo).
- **Frontend**: vanilla JS ES modules (no bundler) + the dope design system,
  embedded in the binary.
- **Crypto**: scrypt KEK (vendored `@noble/hashes`, pure JS, no WASM) +
  AES-256-GCM via WebCrypto.
- **Offline / PWA**: installable, works offline and resyncs on reconnect. A
  service worker caches the app shell; board ciphertext, timelines and the board
  list are mirrored in IndexedDB; offline edits queue in an outbox (entities get
  temporary ids remapped to real server ids on sync). See the *Offline / PWA*
  section in [`AGENTS.md`](AGENTS.md).

See [`AGENTS.md`](AGENTS.md) for the codebase map and [`PLAN.md`](PLAN.md) /
[`OVERVIEW.md`](OVERVIEW.md) for the design.

## Develop

```sh
just dev-web-only   # server only (assets hot-read from disk)
just dev            # server + telegram bot
just test           # go test + node frontend tests
just pre-commit     # fmt + vet + tidy-check + test
just invite 7       # mint a one-shot registration invite
```

Server listens on `$PORT` (default 9673); DB at `$XY_DB` (default `xy.db`).
Config via `.env` (copy from [`.env.example`](.env.example)). Telegram
register/login needs `XY_BOT_SECRET` set on both server and bot.

Bootstrap a password account (registration is otherwise telegram-only):

```sh
printf '<password>' | XY_DB=… xy-server adduser <username>   # password via stdin
```

## docx export (chgksuite)

The "export list to docx" action (list `⋯` menu → **Экспорт в docx**) posts the
list's decrypted card descriptions — plus any images referenced by `(img …)`
directives — to `POST /api/export/docx`. The server composes a `.docx` with the
external [`chgksuite`](https://pypi.org/project/chgksuite/) tool in a scratch
dir, returns it, and wipes the plaintext immediately (the one place plaintext
briefly reaches the server — a tolerated risk, see `PLAN`).

The compose command is configurable via **`XY_CHGKSUITE_CMD`** (space-separated,
default `chgksuite`); the server appends `compose docx --ignore_missing_images
source.4s`. chgksuite must be installed for the feature to work — without it the
endpoint returns a 500 carrying the tool's stderr.

Install it in an isolated venv (example using [`uv`](https://docs.astral.sh/uv/)):

```sh
uv venv /opt/xy/.venv --python 3.12
uv pip install --python /opt/xy/.venv/bin/python chgksuite
# then point the server at it:
#   XY_CHGKSUITE_CMD=/opt/xy/.venv/bin/chgksuite
```

### On the production host (xy.pecheny.me)

Prod runs `xy.service` (systemd) as the locked-down `xy` user
(`ProtectHome=true`, `ProtectSystem=strict`, `PrivateTmp=true`). For the venv to
be executable under that sandbox it must live outside `/home` and be
world-readable:

```sh
# uv-managed CPython in a shared, world-readable dir (xy user can't read /root):
sudo env UV_PYTHON_INSTALL_DIR=/opt/xy/pythons uv python install 3.12
sudo env UV_PYTHON_INSTALL_DIR=/opt/xy/pythons uv venv /opt/xy/.venv --python 3.12
sudo uv pip install --python /opt/xy/.venv/bin/python chgksuite
sudo chmod -R a+rX /opt/xy/pythons /opt/xy/.venv
```

`XY_CHGKSUITE_CMD=/opt/xy/.venv/bin/chgksuite` is set in `/etc/xy.env`. Upgrade
later with `sudo uv pip install --python /opt/xy/.venv/bin/python -U chgksuite`.
chgksuite writes nothing outside the scratch dir, so the `PrivateTmp` sandbox and
`/var/lib/xy` HOME are sufficient (no extra `ReadWritePaths` needed).
