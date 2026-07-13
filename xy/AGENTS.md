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
  **No external runtime dependencies**: docx/import/handouts are all in-process,
  and typst is linked in as a wasm module run under wazero (pure Go, so the binary
  stays CGO_ENABLED=0 and cross-compilable).
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
  export.go            POST /api/export/{docx,pdf} — one 4s source + images, exported two ways, both fully
                       in-process (chgk/docx, chgk/typstdoc), images included; no Python. The PDF goes through
                       the shared typst (wasm) pool (typst.go), so it too writes nothing anywhere
  import4s.go          POST /api/import/parse — .4s/.zip/.docx → 4s source + images (chgk/chgkimport),
                       parsed in memory, nothing persisted; the client encrypts the result into a new list
  handouts.go          POST /api/handouts/{pdf,split_fit} — fully in-process (chgk/handout + typst as a wasm module, see typst.go). No Python, no typst binary, nothing written to disk. Normalize CRLF→LF first (browsers send multipart text as CRLF, which broke the .hndt "---" splitter)
  typst.go             the shared typst (wasm) pool: built once, warmed at boot, injectable so handler tests stub it. XY_WASM_CACHE must be persistent (~15s cold compile vs ~0.6s cached)
  staging.go           handout image staging: /api/handouts/{stage,heartbeat,DELETE stage} — client uploads referenced images once on modal open; pdf/split_fit reuse them via a session id (reaped after ~1min of no heartbeat) instead of re-uploading each generate. Staged images live in memory only, never on disk
  multipart.go         readMultipart: in-memory multipart parsing for every endpoint that receives plaintext (export/handouts/staging/import). ParseMultipartForm spills parts over its budget into an unmanaged temp file — plaintext on disk is exactly what xy must not do. (attachments.go still uses it: those uploads are ciphertext.)
  debug.go             [timing] logs on export/handout endpoints, gated by XY_DEBUG_TIMING
internal/chgk/         Go port of chgksuite's core (xy no longer shells out to Python for docx/handouts)
  fsource/             the "4s" format, both ways: parse.go = parse_4s (oracle-tested vs
                       chgksuite --debug), compose.go = compose_4s (structure → 4s text)
  typo/                typotools.py: the typography pass (quotes/dashes/stress accents/
                       %-decoding) + URL-aware underscore escaping
  docxread/            .docx → plain text — a hand-rolled python-docx (zip/OPC, runs,
                       hyperlinks, numbering, tables, image extraction, in memory, no fs)
  textparse/           parser.py's ChgkParser: plain text → structure. A literal port,
                       quirks included (see the comments); chgk game only, no si/troika
  chgkimport/          the import entry point: .docx/.4s/.zip → 4s source + its images.
                       Byte-parity with chgksuite's `parse` on all 12 chgk .docx fixtures
  handout/             .hndt → .typ (byte-exact vs chgksuite) → PDF via typst; embeds the typst template + Noto Sans.
                       typesetter.go: the Typesetter interface. The server uses the wasm one (typstwasm), so
                       nothing is written anywhere. CLITypesetter drives the typst binary and is kept ONLY as the
                       oracle the wasm path is checked against (wasm_parity_test.go: the fitted row counts must
                       match, since split_fit binary-searches them).
                       splitfit.go: `handouts split_fit` port — per-block binary-search row fit using typst's own pagination (typst query page count, not pypdf), per-question + all-q PDFs, pdfcpu compress; ~12× faster, row counts match chgksuite. (image-shrink refinement not yet ported)
  typstwasm/           typst linked in as a library, compiled to wasm32-wasip1, run under wazero with its
                       World (= typst's filesystem abstraction) served from memory. Removes the last place xy
                       had to hand decrypted questions to a filesystem. A pool of instances, since split_fit
                       fits blocks in parallel; fonts parsed once, images once per generation.
                       ~8× faster per probe than spawning the CLI (1.4ms vs 11.3ms).
                       typst.wasm is //go:embed-ed but NOT in git (30 MB): `just build-wasm` compiles
                       typst-wasm/ (Rust) into it — once per clone, then only on a typst bump. Every Go
                       recipe (build/dev/test) depends on a guard that says so if the file is missing.
  docx/                parsed structure → .docx (OOXML), reusing chgksuite's template.docx; byte-parity tested (document.xml body + rels: spacing, run boundaries, hyperlinks) vs chgksuite.
                       (img …) images go through imgconv.ForExport like the PDF's — see below (images.go)
  typstdoc/            parsed structure → .typ → PDF via typst (the same wasm pool handouts use): the docx
                       export in the other format. template.docx's page setup transcribed into the preamble
                       (A4, 1"/0.75" margins, 12pt body, 16/14pt headings, no auto-hyphenation, page number
                       bottom-left) and Word's keeps mapped onto typst blocks: keepLines → breakable: false,
                       keepNext → sticky, pageBreakBefore → #pagebreak(weak: true). Arial → Noto Sans (the
                       faces already embedded for handouts). Emitted in typst CODE mode — every piece of
                       editorial text is a typst string literal inside text("…"), so a question full of typst
                       syntax is just characters. Two things typst forced: a pagebreak may not sit inside a
                       block (a mid-question (PAGEBREAK) splits the block in two), and a line box is measured
                       cap-height→baseline by default, which makes Word's flush paragraphs overlap by a
                       descender — hence top-edge/bottom-edge + leading: 0 in the preamble. Noto Sans has no
                       `smcp`, so (sc …) small caps are synthesized (upper + 0.8em), as Word synthesizes them.
  inline/              the 4s inline layer BOTH exporters share, so the .docx and the .pdf agree
                       character-for-character: markup tokenizing (bold/italic/img/screen/hyperlink…),
                       backtick stress accents, the non-breaking space/hyphen gluing, and (img …) sizing.
                       Lifted out of docx/ when typstdoc needed it — do not fork it back.
  imgconv/             ForExport: encode a picture for the size it is DRAWN at — downscale to 200 dpi of that
                       size (never up), JPEG q85 unless it has transparency (then PNG). Both exporters use it.
                       Re-encoding is unavoidable (neither Word nor typst reads WebP), but re-encoding a photo
                       as PNG is lossless and huge: an 800 KB JPEG attachment used to come back as a megabyte
                       of PNG — most of the exported file. Don't "simplify" this back to a plain ToPNG.
  *_test.go            full-flow integration test (register→board→card→label→timeline+ACL)
internal/session/      cookie + session.User (ported from dope/platform/session)
internal/blobstore/    attachment bytes ON DISK (content-addressed, sharded, write-once); the DB
                       stores only a blob_ref. NB: backups therefore have two halves — litestream
                       replicates xy.db, an hourly `rclone copy` replicates blobs/. Restore the DB
                       alone and every attachment is a dangling ref. See README "Deployment & backups".
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
                       move/copy (by board name + list + position; a copy/cross-board
                       move carries the card's labels + comments + attachments via
                       copyCardExtras — online-only for the extras), list ⋯ menu
                       (incl. rename/delete list), board ☰ menu (incl. rename/delete
                       board, owner-only delete), export to docx / PDF (same request,
                       `exportList(list, format)`); direct links to a card
                       (?card=) and a comment (&comment=, copied from the timeline 🔗);
                       «Управление списками» modal groups consecutive lists into a
                       list_of_lists (☰ menu); all mutations via sync.js (offline-capable);
                       «📐 Изменить размеры» (☰ menu) sets three display prefs, stored in
                       localStorage["xy.sizes"] (per browser, all boards) and applied as CSS
                       vars on <html>: --kanban-max-w (the board is a centred column, so a
                       wide monitor doesn't strand the reader at the screen edge),
                       --klist-w, --kcard-lines. Cards hold their FULL text; --kcard-lines
                       line-clamps it (max slider position = no clamp = whole question).
                       Defaults: 1512px (a 14" MacBook's logical width) / 280px / 3 lines.
                       Don't reintroduce a char cap in cardBody — that's what made a card
                       stop at 80 chars no matter how much room the reader gave it;
                       «📥 Импорт» (☰ menu) uploads a .4s/.zip/.docx to /api/import/parse and
                       turns the returned 4s into a new list — one card per blank-line block,
                       each (img …) attached to the card that references it. A .docx (a lossy
                       heuristic parse) first opens the verification screen: editable 4s on the
                       left, the live list preview on the right. .4s/.zip import straight.
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
just build-wasm     # compile typst → internal/chgk/typstwasm/typst.wasm (needs Rust;
                    #   not in git — run once per clone, then only on a typst bump)
just build          # the app (pure Go; embeds the wasm above)
just dev-web-only   # server only (assets hot-read from disk)
just dev            # server + bot
just invite 7       # mint a registration invite
# bootstrap a password account (registration is otherwise telegram-only):
printf '<password>' | XY_DB=… xy-server adduser <username>   # password via stdin
just test           # go test + node frontend tests
# XY_TYPST_TEST_BIN=/path/to/typst  → also runs the typst-CLI parity tests (the
#   oracle the in-process wasm typst is checked against). typst is NOT needed to
#   run xy — only to run those tests.
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
  (encrypt + upload/download/delete; stored as uploaded — WebP q70 recompression
  is an opt-in checkbox, since the exports re-encode pictures for the page anyway);
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
concatenated cards), and per-list export (docx / PDF) / handout generation cover
the whole group when invoked on any member (`exportScope`).

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
