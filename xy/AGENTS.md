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
  lists_cards.go       lists/cards/labels/timeline handlers + DTOs/scanners
  invite.go            invite minting (subcommand)
  export.go            POST /api/export/docx — shells out to chgksuite (XY_CHGKSUITE_CMD)
  *_test.go            full-flow integration test (register→board→card→label→timeline+ACL)
internal/session/      cookie + session.User (ported from dope/platform/session)
web/assets/            //go:embed static (package assets)
  static/
    crypto.js          envelope format + board key lifecycle + IndexedDB key cache
    rank.js            fractional indexing (LexoRank-style keyBetween)
    app.js             shared fetch/DOM helpers, derived card titles
    diff.js            word-level token diff for desc_edit timeline highlighting
    index.js/.html     board list + create-board (passphrase) flow
    board.js/.html     kanban: unlock, drag-reorder, card detail, timeline, labels,
                       move/copy (by board name + list + position), list ⋯ menu, docx export
    login/register/profile  auth UI (login/menu ported from dope)
    styles.css         dope design system (copied) + xy board/card section at the end
    vendor/            self-hosted @noble/hashes (scrypt + deps), WebCrypto shim
jstest/                node --test: crypto round-trips, rank ordering
```

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
- full API: boards/members/keymeta/lists/cards/labels/timeline/attachments/
  player-map, all behind write-tx + ACL; encrypted blob store;
- kanban UI: unlock, drag-reorder, derived titles, optimistic updates;
- card detail: monospace editor, desc diffs, labels, comments, attachments
  (WebP q70 recompress + encrypt + upload/download/delete);
- test lists/cards (datetime title, player-id description, auto green/red labels);
- cross-board copy/move with client-side re-encryption + label reconcile;
- `deploy.py` SSH template.

Test coverage: Go integration tests (`internal/server/*_test.go`) cover the full
register→board→card→label→timeline→attachment→player-map flow + ACL rejection;
node tests (`jstest/`) cover crypto round-trips/tamper/rewrap and rank ordering.

**Not yet browser-E2E tested** (no headless browser in the build env): the JS
modules are syntax-checked and the crypto/rank logic is unit-tested, but the
full board/card UI flows should be click-tested in a browser before release.

**Later phases** (PLAN §8): offline/PWA, encrypted client-side search, Trello
API compatibility, chgksuite import/export.
```
