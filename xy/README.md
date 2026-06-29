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

### Browser testing

There is no `playwright`/`puppeteer` npm package, but Playwright's Chromium
binaries are cached under `~/.cache/ms-playwright/`. Drive them over CDP with
Node's built-in `WebSocket` (Node 24, no deps): launch `chrome-headless-shell`
with `--remote-debugging-port=9222 --user-data-dir=…`, `fetch` a tab from
`http://127.0.0.1:9222/json/new?<url>`, then issue `Page.navigate`,
`Runtime.evaluate` (with `awaitPromise` for async page code) and
`Page.captureScreenshot`. Run the **built binary from `/tmp`** (not the repo dir)
so `staticSource()` falls back to embed mode with `?v=` asset versioning and
ETags, matching production. The `/profile/tokens` UI and the token→Trello-API
flow were verified this way.

Bootstrap a password account (registration is otherwise telegram-only):

```sh
printf '<password>' | XY_DB=… xy-server adduser <username>   # password via stdin
```

## Trello-compatible API (chgksuite)

[`chgksuite`](https://pypi.org/project/chgksuite/) can read from and write to a
Trello board (`chgksuite trello download` / `upload`). xy exposes the exact
slice of the Trello v1 API those commands use, so chgksuite can treat an xy board
as a Trello board:

- `GET /1/boards/{id}` — board with inline `lists[]`, `cards[]`, `labels[]`;
- `GET /1/boards/{id}/lists` — the board's lists;
- `POST /1/lists/{id}/cards` — create a card (form fields `name`, `desc`).

Requests authenticate with `key` + `token` query/form params, mirroring Trello.
The `key` is ignored; the `token` is an **xy API token** minted at
**`/profile/tokens`** (account menu → API-токены). Tokens are valid for one month
and can be revoked at any time; only a salted hash is stored, and the raw value
is shown once.

Because the board is end-to-end encrypted, every text field (board/list/card/
label name and card `desc`) is returned as the **base64 ciphertext envelope** —
the same bytes the web client gets — and is decrypted locally with the board
passphrase (the `crypto.js` envelope format). Uploads are symmetric: `desc` must
already be such an envelope (a plaintext `desc` is rejected), so the server never
sees plaintext. Board/list/card ids are xy's numeric ids as strings.

To point chgksuite at xy, set its Trello API base (the `API` constant in
`chgksuite/trello.py`) to `https://xy.pecheny.me/1`, paste the token when prompted,
and give it a numeric xy board id (the `/board/{id}` path segment).

## docx export & handout PDFs

The "export list to docx" action (list `⋯` menu → **Экспорт в docx**) posts the
list's decrypted card descriptions — plus any images referenced by `(img …)`
directives — to `POST /api/export/docx`. The server composes the `.docx`
**in-process** with the Go port of chgksuite's core (`internal/chgk/docx`,
output byte-for-byte equal to chgksuite's), returns it, and wipes the plaintext
immediately (the one place plaintext briefly reaches the server — a tolerated
risk, see `PLAN`). No external tool is involved, so there is nothing to install.

Handout PDFs (**генерация раздаток**, `POST /api/handouts/{pdf,split_fit}`) are
also produced in-process: the `.hndt` source is rendered to `.typ` by
`internal/chgk/handout` and typeset with [**typst**](https://typst.app/). typst
is the only external runtime dependency; the font and template assets are
embedded in the binary. Point the server at the binary with **`XY_TYPST_CMD`**
(default `typst`):

```sh
# download a standalone typst release and:
#   XY_TYPST_CMD=/path/to/typst
```

On the production host (xy.pecheny.me) `XY_TYPST_CMD` is set in `/etc/xy.env` to a
standalone typst binary under `/var/lib/xy`. xy is otherwise fully self-contained
(pure Go, no Python) — see [`AGENTS.md`](AGENTS.md).
