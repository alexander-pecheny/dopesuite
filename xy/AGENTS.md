# xy — Codebase Map

## What this is
A Trello-style board app for ЧГК (trivia) question editing. Every piece of
user-entered data (board/list/card/label/comment/attachment) is **encrypted
client-side** with a per-board passphrase; the server only ever stores and
serves ciphertext plus the structural metadata needed to order, sync, and
authorize. Built by reusing patterns and frontend assets from `~/dope`
(see `OVERVIEW.md`, `PLAN.md`). Russian-language UI.

## Stack
- **Backend**: Go 1.26, SQLite (WAL, `modernc.org/sqlite`, pure Go, no cgo).
- **Frontend**: vanilla JS ES modules (no bundler) + the dope design system
  (`styles.css`), embedded in the binary.
- **Crypto**: scrypt KEK (vendored `@noble/hashes`, pure JS, **no WASM** → runs
  under iOS Lockdown Mode) + native AES-256-GCM via WebCrypto.
- **Tests**: Go (`go test`) + frontend (`node --test jstest/*.test.js`).
- **Build/run**: `justfile`.

## Layout
```
cmd/xy-server/         thin main() → server.Main(); also `xy-server invite [days]`
cmd/telegram-bot/      login bot, bridges to server via shared secret (no DB handle)
internal/server/       package server — the whole HTTP server
  server.go            DB open (BuildDSN/WAL), write-tx discipline (conn-before-lock)
  db.go                full schema + migration runner (schema_versions)
  main.go              mux wiring (Go 1.22 method+pattern routes), invite subcmd
  assets.go            embed/disk asset serving, ETag cache-busting, gzip, CSP, page serve
  http.go              writeJSON/readJSON/httpError
  errors.go            appError → status mapping
  auth.go              sessions, login/register/password, telegram bridge
  boards.go            boards CRUD, keymeta (passphrase re-wrap), members, ACL helpers
  lists_cards.go       lists/cards/labels/timeline + list-group handlers, DTOs/scanners
  tokens.go            API tokens: month-lived bearer creds (manage at /profile/tokens)
  trello_compat.go     Trello-compatible API for chgksuite (token-authed via key+token)
  rank.go              server-side fractional-index keyAfter (Trello card upload)
  invite.go            invite minting (subcommand)
  admin.go             /admin + /admin/create_users (gated on XY_ADMIN_USER, default "pecheny")
  export.go            POST /api/export/docx — shells out to chgksuite (XY_CHGKSUITE_CMD)
  *_test.go            full-flow integration test (register→board→card→label→timeline+ACL)
internal/session/      cookie + session.User (ported from dope/platform/session)
web/assets/            //go:embed static (package assets)
  static/
    crypto.js          envelope format + board key lifecycle + IndexedDB key cache
    store.js           offline IndexedDB layer: snapshot/timeline/attachment mirror,
                       mutation outbox, temp-id↔real-id map (DB "xy-offline")
    sync.js            offline engine: mutate()/flush() outbox replay with negative
                       temp-id remapping, snapshot apply, pending-timeline synthesis,
                       online/offline status events (PWA resync)
    sw.js              service worker — app-shell caching (served at root, scope '/')
    manifest.webmanifest  PWA manifest (served at root); icons icon-*.png/apple-touch-icon
    rank.js            fractional indexing (LexoRank-style keyBetween)
    app.js             shared fetch/DOM helpers, derived titles, offline-tolerant requireLogin
    diff.js            word-level token diff for desc_edit timeline highlighting
    index.js/.html     board list + create-board (passphrase) flow; offline board-list cache
    board.js/.html     kanban: unlock, drag-reorder, card detail, timeline, labels,
                       move/copy (by board name + list + position), list ⋯ menu, docx export;
                       «Управление списками» modal groups consecutive lists into a
                       list_of_lists (☰ menu); all mutations via sync.js (offline-capable)
    menu.js            theme boot + ☰ menu; also injects PWA <head> tags + registers sw.js
    login/register/profile  auth UI (login/menu ported from dope)
    tokens.js/.html    /profile/tokens — create/revoke API tokens for the Trello API
    styles.css         dope design system (copied) + xy board/card section at the end
    vendor/            self-hosted @noble/hashes (scrypt + deps), WebCrypto shim
jstest/                node --test: crypto round-trips, rank ordering, offline sync engine
```

## Offline / PWA (PLAN §8)
The app is an installable PWA that works offline and resyncs on reconnect.
- **App shell**: `sw.js` (served at `/sw.js`, scope `/`) precaches the static
  assets + page routes; navigations are network-first→cache, versioned `?v=`
  assets cache-first, others stale-while-revalidate. `/api/*` is never SW-cached.
- **Data mirror**: `store.js` keeps a per-board ciphertext snapshot, per-card
  timelines, the board list and downloaded attachment bytes in IndexedDB
  (DB `xy-offline`). Everything stored is ciphertext (same as the server); the
  cached DK in `xy-keys` is what decrypts it. No plaintext is persisted.
- **Outbox + resync**: every board mutation flows through `sync.js#mutate`. Online
  with an empty queue it's sent immediately; otherwise it's queued. Entities
  created offline get **negative temp ids** (which flow transparently through the
  numeric-id code in board.js); on `flush` each create's response yields temp→real,
  and later ops have their temp-id references (URL path + JSON body) rewritten
  before sending. After a board's queue drains, the UI reloads a fresh snapshot.
  Cross-board copy/move, board creation, and attachment upload/delete stay online-only.

## Crypto model (see PLAN §2)
Each board has a random 32-byte data key (DK). The passphrase derives a KEK
(scrypt) that only wraps/unwraps DK; a `verify_token` lets the client confirm a
passphrase on unlock. Changing the passphrase re-wraps DK (no data re-encrypt).
DK is cached per board in IndexedDB. Wire envelope: `magic("xy1") | alg(1) |
nonce(12) | ct+tag`, base64 over JSON. `crypto.js` is the sole owner of this
format. **XSS = total compromise**, so the app serves a strict CSP (script-src
'self', no inline/eval/wasm, no third-party origins); the one JS dependency is
vendored same-origin under that CSP.

## Run / build / test
```
just dev-web-only   # server only (assets hot-read from disk)
just dev            # server + bot
just invite 7       # mint a registration invite
# bootstrap a password account (registration is otherwise telegram-only):
printf '<password>' | XY_DB=… xy-server adduser <username>   # password via stdin
just test           # go test + node frontend tests
just pre-commit     # fmt + vet + tidy-check + test
```
Server listens on `$PORT` (default 9673); DB at `$XY_DB` (default xy.db).
Config via `.env` (see `.env.example`). Telegram register/login needs
`XY_BOT_SECRET` set on both server and bot.

## Conventions
- **Reuse the design system** (`styles.css` CSS variables, components) — extend
  it, don't inline one-off styles. Frontend modules attach to `window.xy*` and
  also `export`.
- **Write discipline**: every mutation goes through `s.withWriteTx` (pulls the
  pooled conn before the lock, bounds the tx). Ported from dope.
- **Server never sees plaintext**: content columns are `_enc` BLOB envelopes;
  handlers validate structure + ACL only.

## Milestone 1 status
**Functionally complete.** Built & tested:
- scaffold, auth (password + telegram bridge + bot binary), session middleware;
- client crypto (`crypto.js`) + IndexedDB key cache; fractional ranks (`rank.js`);
- full API: boards/members/keymeta/lists/cards/labels/timeline/attachments,
  all behind write-tx + ACL; encrypted blob store;
- kanban UI: unlock, drag-reorder, derived titles, optimistic updates;
- card detail: monospace editor, desc diffs, labels, comments, attachments
  (WebP q70 recompress + encrypt + upload/download/delete);
- test lists/cards (datetime title, tester list — players/teams, auto green/red labels);
- cross-board copy/move with client-side re-encryption + label reconcile;
- `deploy.py` SSH template.

Test coverage: Go integration tests (`internal/server/*_test.go`) cover the full
register→board→card→label→timeline→attachment flow + ACL rejection;
node tests (`jstest/`) cover crypto round-trips/tamper/rewrap and rank ordering.

node tests also cover the offline sync engine (temp-id remapping, snapshot apply,
and a full offline→online resync against an in-memory IndexedDB).

**Browser testing**: a headless browser *is* available — Playwright's Chromium
binaries are cached under `~/.cache/ms-playwright/` (no `playwright`/`puppeteer`
npm package). Drive it over CDP with Node's built-in `WebSocket` (no deps): launch
`chrome-headless-shell` with `--remote-debugging-port`, `fetch` a tab from
`/json/new?<url>`, then `Page.navigate`/`Runtime.evaluate`/`Page.captureScreenshot`.
Run the built binary from `/tmp` (not the repo dir) to get embed mode + `?v=`
asset versioning. The `/profile/tokens` page + token→Trello-API flow were verified
this way. Still worth a manual pass before release: the full board/card UI flows
and service-worker install/offline behaviour.

**Later phases** (PLAN §8): ~~offline/PWA~~ (done — see "Offline / PWA" above),
~~Trello API compatibility~~ (done — see `trello_compat.go`: the read+upload
surface chgksuite's `trello.py` uses, token-authed; text fields return as the
base64 ciphertext envelope, decrypted locally with the board passphrase),
encrypted client-side search, chgksuite import/export.

## List groups (list_of_lists)
A named, ordered run of **consecutive** lists, sharing one question-numbering
sequence and a combined export. Schema: `list_groups(name_enc)` + nullable
`lists.group_id` (migrateV6); the board snapshot adds a `groups[]` array and each
list carries `group_id`. Endpoints: `POST /api/boards/{id}/list-groups`
{name_enc, list_ids} (≥2 lists, folds them in), `PATCH /api/list-groups/{id}`
(rename), `DELETE` (dissolve → members released to group_id NULL). The
«Управление списками» modal (☰ menu, `board.js`) is the editing surface: one row
per list, drag / position-input reorder, multi-select move-together, and
🔗 Связать when the checked rows are consecutive ungrouped lists. Orderable units
are standalone lists and whole groups (a group always moves as one block, keeping
its members consecutive — the invariant the board render relies on; the
single-list move modal refuses to reorder a grouped list on the same board).
On the board a group renders inside one bordered `.kgroup` box with a single
header; numbering flows across the group (`numberQuestionCards` over the
concatenated cards), and per-list docx export / handout generation cover the
whole group when invoked on any member (`exportScope`).

## Trello-compatible API (chgksuite integration)
`trello_compat.go` serves the three Trello calls chgksuite makes, authed by
`key`+`token` query/form params (`key` ignored; `token` is an xy API token):
- `GET /1/boards/{id}` → board with inline `lists[]`/`cards[]`/`labels[]`;
- `GET /1/boards/{id}/lists`;
- `POST /1/lists/{id}/cards` (form `name`,`desc`; `desc` must be a base64
  envelope — symmetric with the download path; `name` ignored as titles derive
  from the description). ids are xy's numeric ids as strings.
Tokens are minted/revoked at `/profile/tokens` (`tokens.go`, `api_tokens` table,
30-day expiry, sha256-hashed like sessions). To point chgksuite at xy, set its
`API` base to `https://xy.pecheny.me/1` and paste the token + a numeric board id.
```
