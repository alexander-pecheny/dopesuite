# xy

A Trello-style board app for ЧГК (trivia) question editing. Every piece of
user-entered data (list/card/label/comment/attachment) is **encrypted
client-side** with a per-board passphrase; the server only stores and serves
ciphertext plus the structural metadata needed to order, sync, and authorize.
**Board names are the one exception** — they're stored in plaintext so the board
list is readable without unlocking every board (see the trust model in `PLAN.md`).

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

Because the board's data is end-to-end encrypted, its encrypted text fields
(list/card/label name and card `desc`) are returned as the **base64 ciphertext
envelope** — the same bytes the web client gets — and are decrypted locally with
the board passphrase (the `crypto.js` envelope format). The **board `name` is
plaintext** (migrated boards; legacy boards still return the ciphertext envelope
until backfilled). Uploads are symmetric: `desc` must already be such an envelope
(a plaintext `desc` is rejected), so the server never sees encrypted content in
the clear. Board/list/card ids are xy's numeric ids as strings.

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
`internal/chgk/handout` and typeset with [**typst**](https://typst.app/) — which is
**linked into the binary as a WebAssembly module**, not shelled out to. There is
nothing to install: xy has no external runtime dependencies at all.

typst's `World` trait is its filesystem abstraction, so `internal/chgk/typstwasm`
serves the source, the fonts and every referenced image from memory. Handout
rendering therefore writes **nothing to disk** — the one place xy used to have to
put the user's decrypted questions on a filesystem. It runs under
[wazero](https://wazero.io), a pure-Go WASM runtime, so the binary stays
`CGO_ENABLED=0` and cross-compilable.

**`XY_WASM_CACHE`** is where wazero caches typst compiled to machine code. It must
live on **persistent** storage: compiling the 30 MB module takes ~15 s cold but
~0.6 s from the cache, and the cache survives restarts — put it on tmpfs and every
reboot pays the 15 s again. It contains compiled typst, never user data. Defaults
to `$XDG_CACHE_HOME/xy/typst-wasm`.

The 30 MB artifact is **not in git** — building it is a separate path from
building the app:

```sh
rustup target add wasm32-wasip1   # once
just build-wasm                   # compiles typst-wasm/ → internal/chgk/typstwasm/typst.wasm
just build                        # the app; embeds the wasm built above
```

`just build-wasm` is the only step that needs Rust, and the only step that has to
be repeated when bumping typst (`typst-wasm/Cargo.toml`). Everything else — build,
dev, test — is pure Go and just embeds whatever `typst.wasm` is sitting there; the
recipes fail with an instruction if it is missing.

The release profile links with **thin LTO** (~4 min). Fat LTO peaks at ~4.9 GB RSS
and gets rustc OOM-killed with no message (a bare `SIGKILL`) on an 8 GB machine with
no swap, and it buys very little: measured against the thin build, per-render probe
time is identical (~1.9 ms, within noise) and only pool startup is ~9% faster
(627 ms → 576 ms, from a 3 MB smaller module) — once per server restart, not per
request. On a machine with the RAM for it: `CARGO_PROFILE_RELEASE_LTO=fat just build-wasm`.

`internal/chgk/handout`'s `CLITypesetter` still drives the standalone typst binary,
but only as the **oracle** the wasm path is tested against (`wasm_parity_test.go`
requires the fitted `split_fit` row counts to match) — it is on no request path.
Set `XY_TYPST_TEST_BIN` to a typst binary to run those tests.

## Deployment & backups

**Attachment bytes are files on disk, not rows in SQLite** (`internal/blobstore`:
content-addressed, sharded, write-once). The DB only stores a `blob_ref`. So a
backup has **two halves, and a restore needs both** — restore `xy.db` alone and
every attachment becomes a dangling ref:

| what | how | where |
| --- | --- | --- |
| `xy.db` | litestream (continuous) | `r2:backups/xy/xy.db` |
| `blobs/` | `rclone copy`, hourly systemd timer | `r2:backups/xy/blobs` |

The `xy.db` half is mostly ciphertext, but note that **board names are plaintext**
(see the trust model) — they, and the structural metadata, are visible to whoever
holds the R2 backup. All other user content remains encrypted at rest and in R2.

Restore both:

```sh
litestream restore -o /var/lib/xy/xy.db r2:backups/xy/xy.db
set -a; . /etc/xy-blobs-backup.env; set +a
RCLONE_CONFIG=/dev/null rclone copy r2:backups/xy/blobs /var/lib/xy/blobs
chown -R xy:xy /var/lib/xy
```

Two deliberate choices worth knowing:

- **`rclone copy`, not `sync`.** Blobs are immutable, so the backup only ever needs
  to grow. `sync` would mirror deletions — one accidental attachment delete would
  propagate to R2 and the bytes would be gone for good.
- **Blobs are not stored in SQLite**, which would have given one backup mechanism
  instead of two. litestream takes timer-driven *full* snapshots: a 1.6 MB `xy.db`
  costs 21.5 MB in R2 (13 daily snapshots × ~1 MB, at a 14-day retention) — a ~13×
  amplification. Ciphertext does not compress, so blobs in the DB would be
  re-uploaded in full on every snapshot; `rclone copy` of an immutable
  content-addressed tree uploads each blob exactly once, ever.
