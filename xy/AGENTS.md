# xy — Codebase Map

## What this is
A Trello-style board app for ЧГК (trivia) question editing. Every piece of
user-entered data (list/card/label/comment/attachment) is **encrypted
client-side** with a per-board passphrase; the server only ever stores and
serves ciphertext plus the structural metadata needed to order, sync, and
authorize. **Board names are the one exception** — plaintext, server-visible, so
the board list is readable without unlocking each board (per-board
`schema_version`: 1 = legacy `name_enc`, 2 = plaintext `name`; legacy boards
backfilled lazily via `POST /api/boards/{id}/migrate-name`; see `migrateV10`). Built by reusing patterns and frontend assets from `../dope`, its
sibling in the dopesuite monorepo (the root `AGENTS.md` has the monorepo rules).
Russian-language UI.

## Stack
- **Backend**: Go 1.26, SQLite (WAL, `modernc.org/sqlite`, pure Go, no cgo).
  **No external runtime dependencies**: docx/import/handouts are all in-process,
  and typst is linked in as a wasm module run under wazero (pure Go, so the binary
  stays CGO_ENABLED=0 and cross-compilable).
- **Frontend**: strict-TypeScript ES modules (root ADR-0001) + the DopeUIKit
  design system, embedded in the binary. Sources in `web/ts/*.ts`; the
  shared root toolchain (`just build-web`, esbuild per-file transform + native
  tsc) emits same-named ESM into the gitignored `web/assets/static/dist/`,
  which the pages load and the SW precaches. board.ts is a thin orchestrator
  over extracted kernels (unlock.ts, dragrank.ts, carddetail.ts, carddraft.ts,
  timeline.ts, boardmembers.ts, handoutsession.ts — each a `create(deps)`
  factory with jstest coverage).
- **Crypto**: scrypt KEK (vendored `@noble/hashes`, pure JS, **no WASM** → runs
  under iOS Lockdown Mode) + native AES-256-GCM via WebCrypto.
- **Tests**: Go (`go test`) + frontend (`deno test --parallel jstest/`).
- **Build/run**: `justfile`.
- **UI markup**: no hand-written HTML (or CSS classes) anywhere. **DopeUIKit**
  (`pecheny.me/dopeuikit`, vendored via `replace => ../dopeuikit`) has two layers:
  `ui/` is the generic DSL **engine** (parser, validator, expansion framework,
  printer, builder machinery, codegen — no vocabulary, no CSS class names) and
  `kit/` is the shared **design system** (core vocab + expanders + Chrome +
  generated builder + `core.css`/fonts). `internal/ui` is xy's thin **overlay**
  on the `kit` (imports `pecheny.me/dopeuikit/kit`). Pages are authored in
  `.dopeui` (`web/assets/ui/`) as typed AppKit-style primitives — `page`,
  `topbar`, `col`/`row`, `button`, `modal`, `mount`… — compiled to HTML at server
  startup by the xy `App` (`internal/ui/app.go`, `Compile`); the dynamic /admin
  pages use the same package's builder (`Render`). The overlay adds xy primitives
  (`docoverlay`/`headrow`/`headactions`/`split`/`pane`/`previewtitle`), overrides
  `checkbox`/`editor`, and supplies the board mount kinds + PWA chrome
  (`internal/ui/vocab.json`, `expand.go`). The vocabulary is closed; unknown
  primitive/prop, bad enum value, or duplicate id is a compile error. Spec:
  DopeUIKit `DESIGN.md` (engine + kit) + `internal/ui/DESIGN.md` (xy overlay).
- **CSS**: the shared design system is DopeUIKit's `assets/core.css` (served via
  `kit.CoreCSS`); xy's `web/assets/static/styles.css` is only the xy layer
  (kanban/card/board + xy vars + PWA overrides). The server serves
  `/static/styles.css` as core + xy concatenated; `/static/fonts/*` come from the
  kit (`kit.Fonts`, no local font copies).

## Layout
```
cmd/xy-server/         thin main() → server.Main(); also `xy-server invite [days]`
cmd/telegram-bot/      login bot, bridges to server via shared secret (no DB handle)
cmd/uic/               compile one .dopeui page to HTML on stdout (xy overlay; debug/diff tool)
internal/ui/           xy's overlay on DopeUIKit's kit: overlay vocab.json (xy primitives +
                       enum extensions), expand.go (checkbox/editor overrides + xy
                       primitives + mount kinds), app.go (builds the xy App via kit.NewApp
                       + Chrome, Compile/Render), generated tags_gen.go (go:generate via
                       cmd/uigen -overlay -base .../kit). DESIGN.md documents the overlay;
                       the DSL engine (ui/) + design system (kit/) + spec live in ../dopeuikit
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
  admin.go             /admin + /admin/create_users (gated on XY_ADMIN_USER, default "pecheny"); pages built with the typed ui builder
  export.go            POST /api/export/{docx,pdf} — one 4s source + images, exported two ways, both fully
                       in-process (chgk/docx, chgk/typstdoc), images included; no Python. The PDF goes through
                       the shared typst (wasm) pool (typst.go), so it too writes nothing anywhere
  import4s.go          POST /api/import/parse — .4s/.zip/.docx → 4s source + images (chgk/chgkimport),
                       parsed in memory, nothing persisted; the client encrypts the result into a new list.
                       POST /api/import/text — the same pipeline without the file: one card's plain text
                       (a question pasted as prose) → 4s, behind the card editor's →.4s button
  typo.go              POST /api/typo — one card's 4s through the typography pass (chgk/typoedit),
                       behind the editor's «типограф» button. Plaintext in, plaintext out, nothing kept
  handouts.go          POST /api/handouts/{pdf,split_fit} — fully in-process (chgk/handout + typst as a wasm module, see typst.go). No Python, no typst binary, nothing written to disk. Normalize CRLF→LF first (browsers send multipart text as CRLF, which broke the .hndt "---" splitter)
  typst.go             the shared typst (wasm) pool: built once, warmed at boot, injectable so handler tests stub it. XY_WASM_CACHE must be persistent (~15s cold compile vs ~0.6s cached)
  reap.go              the tombstone reaper (ADR-0002): every delete is a 14-day tombstone; an hourly
                       loop (+ `xy-server gc` on demand) hard-deletes expired ones, destroys their blobs,
                       and sweeps orphaned blob files
  staging.go           handout image staging: /api/handouts/{stage,heartbeat,DELETE stage} — client uploads referenced images once on modal open; pdf/split_fit reuse them via a session id (reaped after ~1min of no heartbeat) instead of re-uploading each generate. Staged images live in memory only, never on disk
  multipart.go         readMultipart: in-memory multipart parsing for every endpoint that receives plaintext (export/handouts/staging/import). ParseMultipartForm spills parts over its budget into an unmanaged temp file — plaintext on disk is exactly what xy must not do. (attachments.go still uses it: those uploads are ciphertext.)
  debug.go             [timing] logs on export/handout endpoints, gated by XY_DEBUG_TIMING
internal/chgk/         Go port of chgksuite's core (xy no longer shells out to Python for docx/handouts)
  fsource/             the "4s" format, both ways: parse.go = parse_4s (oracle-tested vs
                       chgksuite --debug), compose.go = compose_4s (structure → 4s text)
  typo/                typotools.py: the typography pass (quotes/dashes/stress accents/
                       %-decoding) + URL-aware underscore escaping
  typoedit/            the editor's «типограф» button: typo (quotes/dashes/%-decoding — every
                       knob but accents, which have their own button) + inline's nbsp/nbhyphen
                       gluing, applied to 4s SOURCE rather than to a field's value.
                       Every line is split at its marker first (fsource.SplitMarker) — a pass
                       let loose on raw 4s reads a list item's leading "-" as a stray hyphen
                       and turns it into an em dash, eating the list
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
internal/blobstore/    attachment bytes ON DISK (random-ref, sharded, write-once); the DB
                       stores only a blob_ref. NB: backups therefore have two halves — litestream
                       replicates xy.db, an hourly `rclone sync --backup-dir` replicates blobs/ (deletions go to a dated trash prefix, pruned after 14d). Restore the DB
                       alone and every attachment is a dangling ref. See README "Deployment & backups".
web/ts/                strict-TS ES-module sources; built by `just build-web` into
                       the gitignored web/assets/static/dist/ (see Stack above)
    crypto.ts          envelope format + board key lifecycle + IndexedDB key cache
    store.ts           offline IndexedDB layer: snapshot/timeline/attachment mirror,
                       mutation outbox, temp-id↔real-id map (DB "xy-offline")
    sync.ts            offline engine: mutate()/flush() outbox replay with negative
                       temp-id remapping, snapshot apply, pending-timeline synthesis,
                       online/offline status events (PWA resync)
    sw.ts              service worker — app-shell caching (served at root, scope '/')
    rank.ts            fractional indexing (LexoRank-style keyBetween)
    app.ts             shared fetch/DOM helpers, derived titles, offline-tolerant requireLogin
    diff.ts            word-level token diff for desc_edit timeline highlighting
    index.ts           board list + create-board (passphrase) flow; offline board-list cache
    board.ts           kanban orchestrator over the extracted kernels (unlock.ts =
                       boot/unlock/snapshot-load, dragrank.ts, carddetail.ts,
                       carddraft.ts = draft/dirty rules, timeline.ts,
                       boardmembers.ts, handoutsession.ts = handout image staging
                       session): drag-reorder, card detail, timeline, labels,
                       the card editor's tools row (under the Просмотр/Поля/Текст tabs):
                       ударение types a stress accent (U+0301) into whichever field the
                       caret was last in — a button steals focus on mousedown, so the field
                       is remembered on focusin, not read at click time; типограф (/api/typo)
                       and, on Текст only, →.4s (/api/import/text) rewrite the WHOLE draft,
                       so they need no caret — in Текст the result is typed back into the
                       editor, in Поля the fields are re-rendered from the new draft. Every
                       edit goes through execCommand("insertText"), the only path that keeps
                       the browser's undo stack (a spliced .value makes Ctrl-Z drop everything);
                       move/copy (by board name + list + position; a copy/cross-board
                       move carries the card's labels + comments + attachments via
                       copyCardExtras — online-only for the extras), list ⋯ menu
                       (incl. rename/delete list), board ☰ menu (incl. rename/delete
                       board, owner-only delete), export to docx / PDF (same request,
                       `exportList(list, format)`); direct links to a card
                       (?card=) and a comment (&comment=, copied from the timeline 🔗);
                       «Управление списками» modal groups consecutive lists into a
                       list_of_lists (☰ menu); all mutations via sync.ts (offline-capable);
                       display sizes (users.sizes, edited on /profile — see profile.ts) are
                       delivered in the board snapshot and applied as CSS vars on <html>:
                       --kanban-max-w (the board is a centred column, so a wide monitor
                       doesn't strand the reader at the screen edge), --klist-w,
                       --kcard-lines. Cards hold their FULL text; --kcard-lines line-clamps
                       it (no clamp = whole question). Don't reintroduce a char cap in
                       cardBody — that's what made a card stop at 80 chars no matter how
                       much room the reader gave it. What a card previews is
                       alias → (question text | answer). The alias is the card's own short
                       label (cards.alias_enc, migrateV12 — its OWN encrypted column, NOT a
                       4s marker: the 4s markers mirror chgksuite byte-for-byte, so a marker
                       of ours would break import/export parity or leak into exports). Being
                       no part of the 4s, its input sits BELOW the view panels, between Метки
                       and Вложения (#cardAlias, read by captureDraft in every view) rather
                       than inside Поля, and it wins on every card kind whenever set. It is
                       the one control down there without data-edit-only: the rest need a
                       persisted card, an alias does not, so it stays usable while creating one. The question/
                       answer fallback is the reader's own choice (users.card_title,
                       migrateV13, edited on /profile); an answerless question falls back to
                       its text rather than previewing blank. The snapshot also carries the caller's
                       default_author, pre-filled into new question cards (the Поля Автор
                       field and the Текст stub's "@" line — an untouched stub saves as a
                       card with just that, deliberately). Автор/Источник inputs
                       autocomplete from the board's existing values via suggestWrap, a
                       hand-drawn dropdown (<datalist> never opens on iOS Safari);
                       «📥 Импорт» (☰ menu) uploads a .4s/.zip/.docx to /api/import/parse and
                       turns the returned 4s into a new list — one card per blank-line block,
                       each (img …) attached to the card that references it. A .docx (a lossy
                       heuristic parse) first opens the verification screen: editable 4s on the
                       left, the live list preview on the right. .4s/.zip import straight.
    pwa.ts             PWA boot on every page: manifest/install <head> tags + sw
                       registration + zoom lockdown (theme boot + ☰ menu come from
                       the kit's shared menu module)
    timer.ts           floating ЧГК play timer (⏰ in the board header): question
                       minute + 10s answer countdown, WebAudio bell cues
    chgk.ts            client-side 4s parser for card previews (display-only,
                       never rewrites the source)
    import.ts          Trello board import → new encrypted board (implicit OAuth,
                       server proxy /api/import/trello/proxy, comments past the
                       1000-action cap, attachments); the pure Trello-card→xy-card
                       rules live in trellomodel.ts (jstest-covered, no DOM)
    wordlist.ts        EFF diceware list for generated passphrases (data only)
    profile.ts         /profile: username set-once, logout, and four dialogs — change
                       password, board sizes (three sliders + a to-scale pseudo-board
                       preview, wireframe bars for text; defaults 1512px / 280px / 3
                       lines, max slider position = unlimited/null; debounced POST
                       /api/auth/sizes), default author (POST /api/auth/default-author),
                       card title (POST /api/auth/card-title — question text vs answer).
                       Shared defaults/ranges/sanitize/apply live in app.ts (xySizes)
                       so this write path and board.ts's read path agree
    tokens.ts          /profile/tokens — create/revoke API tokens for the Trello API
web/assets/            //go:embed static + ui (package assets)
  ui/                  the 6 app pages as .dopeui (index, board, login, profile,
                       tokens, import) — compiled to HTML by internal/ui at server
                       startup (per-request in dev disk mode)
  static/              built dist/ (gitignored ESM output), icons +
                       manifest.webmanifest, ding.mp3, plus:
    styles.css         the xy-only CSS layer (kanban/card/board + xy vars + PWA
                       overrides); the shared design system is DopeUIKit's core.css,
                       served concatenated ahead of it (see Stack → CSS)
    vendor/            self-hosted @noble/hashes (scrypt + deps), WebCrypto shim
jstest/                deno test: crypto round-trips, rank ordering, offline sync engine
```

## Offline / PWA
The app is an installable PWA that works offline and resyncs on reconnect.
- **App shell**: `sw.js` (served at `/sw.js`, scope `/`) precaches the static
  assets + page routes; navigations are network-first→cache, versioned `?v=`
  assets cache-first, others stale-while-revalidate. `/api/*` is never SW-cached.
- **Data mirror**: `store.ts` keeps a per-board ciphertext snapshot, per-card
  timelines, the board list and downloaded attachment bytes in IndexedDB
  (DB `xy-offline`). Everything stored is ciphertext (same as the server) except
  plaintext board names; the cached DK in `xy-keys` decrypts the rest. No
  encrypted *content* is persisted in the clear.
- **Outbox + resync**: every board mutation flows through `sync.ts#mutate`. Online
  with an empty queue it's sent immediately; otherwise it's queued. Entities
  created offline get **negative temp ids** (which flow transparently through the
  numeric-id code in board.ts); on `flush` each create's response yields temp→real,
  and later ops have their temp-id references (URL path + JSON body) rewritten
  before sending. After a board's queue drains, the UI reloads a fresh snapshot.
  Cross-board copy/move, board creation, and attachment upload/delete stay online-only.

## Crypto model
Each board has a random 32-byte data key (DK). The passphrase derives a KEK
(scrypt) that only wraps/unwraps DK; a `verify_token` lets the client confirm a
passphrase on unlock. Changing the passphrase re-wraps DK (no data re-encrypt).
DK is cached per board in IndexedDB. Wire envelope: `magic("xy1") | alg(1) |
nonce(12) | ct+tag`, base64 over JSON. `crypto.ts` is the sole owner of this
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
just test           # go test + deno frontend tests
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
  it, don't inline one-off styles. Frontend modules are strict-TS ES modules in
  `web/ts/` — exports are the wiring, no `window.xy*` globals; the jstest suite
  imports the built `static/dist/*.js`.
- **Write discipline**: every mutation goes through `s.withWriteTx` (pulls the
  pooled conn before the lock, bounds the tx). Ported from dope.
- **Server never sees plaintext content**: content columns are `_enc` BLOB
  envelopes; handlers validate structure + ACL only. The lone plaintext exception
  is `boards.name` (a deliberate carve-out — see "What this is").

## Testing
Go integration tests (`internal/server/*_test.go`) cover the full
register→board→card→label→timeline→attachment flow + ACL rejection;
deno tests (`jstest/`) cover crypto round-trips/tamper/rewrap, rank ordering,
and the offline sync engine (temp-id remapping, snapshot apply, and a full
offline→online resync against an in-memory IndexedDB).

**Browser testing**: use the `verify` skill (repo root `.claude/skills/verify/`) —
`agent-browser` drives a persistent headless Chrome from the shell, with the xy
login / board-create / unlock / card flows and their gotchas documented there.
Run the built binary from `/tmp` (not the repo dir) to get embed mode + `?v=`
asset versioning. Still worth a manual pass before release: the full board/card
UI flows and service-worker install/offline behaviour.

**Not built yet**: encrypted client-side search (an IndexedDB index built from
decrypted content as boards are opened; server-side encrypted search is out of
scope).

## List groups (list_of_lists)
A named, ordered run of **consecutive** lists, sharing one question-numbering
sequence and a combined export. Schema: `list_groups(name_enc)` + nullable
`lists.group_id` (migrateV6); the board snapshot adds a `groups[]` array and each
list carries `group_id`. Endpoints: `POST /api/boards/{id}/list-groups`
{name_enc, list_ids} (≥2 lists, folds them in), `PATCH /api/list-groups/{id}`
(rename), `DELETE` (dissolve → members released to group_id NULL). The
«Управление списками» modal (☰ menu, `board.ts`) is the editing surface: one row
per list, drag / position-input reorder, multi-select move-together, and
🔗 Связать when the checked rows are consecutive ungrouped lists. Orderable units
are standalone lists and whole groups (a group always moves as one block, keeping
its members consecutive — the invariant the board render relies on; the
single-list move modal refuses to reorder a grouped list on the same board).
On the board grouped lists render as ordinary columns, each with a small
`🔗group-name` tag underneath (`.klist-group-tag`); numbering flows across
the group (`numberQuestionCards` over the concatenated cards), and per-list
export (docx / PDF) / handout generation cover the whole group when invoked on
any member (`exportScope`).

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
