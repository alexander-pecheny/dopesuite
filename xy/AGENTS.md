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
  which the pages load and the SW precaches. board.ts (≈1700 lines) is the
  board page's boot, render loop, drag, list preview and card pickers; every
  feature the ☰ and the list ⋯ menus offer is its own module registered in the
  panel registry (panels.ts), and the card, the лента, attachments, unlock and
  the rest are `create(deps)` kernels with jstest coverage. Its map is below.
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
  `topbar`, `crumbs`, `col`/`row`, `button`, `modal`, `mount`… — compiled to HTML at server
  startup by the xy `App` (`internal/ui/app.go`, `Compile`); the dynamic /admin
  pages use the same package's builder (`Render`). The overlay adds xy primitives
  (`docoverlay`/`headrow`/`headactions`/`split`/`pane`/`previewtitle`), overrides
  `checkbox`/`editor`, and supplies the board mount kinds + PWA chrome
  (`internal/ui/vocab.json`, `expand.go`). The vocabulary is closed; unknown
  primitive/prop, bad enum value, or duplicate id is a compile error. Spec:
  DopeUIKit `DESIGN.md` (engine + kit) + `internal/ui/DESIGN.md` (xy overlay).
- **Header path**: every page's `topbar` carries a `crumbs` trail — 🏠 / доска,
  🏠 / Профиль / API-токены — replacing the old 🏠 + title pair. An open card is
  a modal, not a place, so it adds no crumb. The primitive and its CSS are the
  kit's; dope renders the same trail.
- **Before adding UI, run the `design-review` skill.** `.dopeui` is a closed,
  compile-checked vocabulary, but `mount` is a hole in it — 41 mount kinds in xy,
  and every panel body, modal and bar is hand-written TS inside one, where
  nothing checks you. Two habits carry most of it: the kit already ships the
  layout utilities (`.u-col`/`.u-row`/`.u-gap-*`/`.u-align-*`/`.u-justify-*` in
  `core.css`) so a class whose whole body is flex+gap+alignment is re-invention
  (classcheck's layout ratchet now refuses new ones), and **spacing is the
  container's job** — `.hint` carries `margin: 0` on purpose, so a margin on a
  child buys one gap and leaves the rest at zero. `/gallery` (dev only) shows
  every primitive on one page; look there before minting a name, and look at
  your surface beside its twin before you ship it.
- **CSS**: the shared design system is DopeUIKit's `assets/core.css` (served via
  `kit.CoreCSS`); xy's `web/assets/static/styles.css` is only the xy layer
  (kanban/card/board + xy vars + PWA overrides). The server serves
  `/static/styles.css` as core + xy concatenated; `/static/fonts/*` come from the
  kit (`kit.Fonts`, no local font copies).

## Layout
```
cmd/xy-server/         thin main() → server.Main(); also `xy-server invite [days]`
cmd/uic/               compile one .dopeui page to HTML on stdout (xy overlay; debug/diff tool)
cmd/xy-cli/            thin main() → xycli.Run(); `just cli` builds it to ~/.local/bin
cmd/chgksuite/         the Go side of chgksuite's own CLI over internal/chgk (`parse`,
                       `compose docx`, `compose telegram`), checked against the Python tool's
                       output. Nothing in xy uses it; see ../chgksuite_go_rewrite.md
internal/xycli/        xy-cli: the board from the shell, for an agent. A second implementation
                       of the Envelope (scrypt KEK + AES-GCM, parity-tested both ways against
                       crypto.ts) and of the export's 4s assembly (parity corpus from export.ts),
                       over an API-token client (ADR-0015); unlocked data keys live in a 0600
                       state file (ADR-0016). Commands: boards/unlock, board show, list, card
                       (4s on stdin/stdout, always with a desc_edit entry), comment (@mentions
                       resolved), label, search (folded), source, export, attachment
internal/rank/         fractional indexing (keyBetween/After), the Go half of web/ts/rank.ts:
                       the server ranks a Trello upload with it, xy-cli every insert and move
internal/ui/           xy's overlay on DopeUIKit's kit: overlay vocab.json (xy primitives +
                       enum extensions), expand.go (checkbox/editor overrides + xy
                       primitives + mount kinds), app.go (builds the xy App via kit.NewApp
                       + Chrome, Compile/Render), generated tags_gen.go (go:generate via
                       cmd/uigen -overlay -base .../kit). DESIGN.md documents the overlay;
                       the DSL engine (ui/) + design system (kit/) + spec live in ../dopeuikit
internal/server/       package server — the whole HTTP server
  server.go            DB open (BuildDSN/WAL), write-tx discipline (conn-before-lock)
  db.go                the schema as a list: `[]schema.Migration` (dopecore/schema applies them once each, in
                       order); a new step takes the next number and goes at the end;
                       XY_REHEARSE_DB=<snapshot> walks a prod copy through them
  main.go              mux wiring (Go 1.22 method+pattern routes), invite subcmd
  assets.go            kit.Assets + kit.PageSet wiring (embed/disk source, ETags, core+xy css), CSP, page serve
  http.go              writeJSON/readJSON/httpError
  errors.go            appError → status mapping
  auth.go              sessions, password login, the Telegram handshake's adapter (state machine in dopecore/tglogin:
                       xy brings its write tx, its users table, its error text)
  bot.go               the login bot, polling in THIS process (no separate service since 2026-08-31):
                       the conversation is dopecore/tgbot.LoginHandler, the answers are xy's writes
                       under withWriteTx, the DM path is the same client. XY_BOT_TOKEN is the switch;
                       tgbot.AcquirePollLock keeps a second instance on this host off the same token,
                       and tgbot.ErrConflict is what Telegram says when the second one is elsewhere
  boards.go            boards CRUD, keymeta (passphrase re-wrap), members, /api/collaborators (who I share boards with), ACL helpers
  boardinvites.go      invite links (ADR-0017): owner mints/revokes/deletes and decides join requests;
                       the invitee peeks at a code and joins. A link grants membership, never the key
  timeline.go          the Timeline's one writer (insertEvent: every kind's columns; appendEvent for the
                       metadata trail) and one reader (timelineColumns + scanTimelineEvent, readTimeline)
  unread.go            the unread rule as SQL fragments (the two buckets, the watermark, the Mention) that
                       the board list, the snapshot, the activity feed and «Прочитать всё» compose
  access.go            a board's children as one table (child: cards, lists, list_groups, labels,
                       test_sessions); boardOf (path → 404), onBoard (body ref → 400 «с другой
                       доски»), requireChildAccess
  patch.go             patch: the columns a PATCH touched, written as one UPDATE with one updated_at
  lists.go             lists + list groups: DTOs/scanners and handlers
  cards.go             cards: DTOs/scanners, create/patch/delete, card labels, Playings, tour testers
  labels.go            labels CRUD
  comments.go          the Timeline's DTOs and handlers: GET /api/boards/{id}/comments returns every
                       live comment on a board in one response (ciphertext, comments only) — what
                       прогрев indexes; a desc_edit payload carries a whole question's before/after
                       and is deliberately excluded; comments add/patch/delete, mentions, import
  bundle.go            the Bundle's server half (ADR-0013): whole-board timeline and
                       attachment reads (ciphertext) for the export, board-level timeline
                       import (all event kinds, authors matched by username, src→new id map
                       returned so batches chain) for the re-encrypting importer
  tokens.go            API tokens: month-lived bearer creds (manage at /profile/tokens). A token
                       IS the user on every route but the password, the username and /admin
                       (ADR-0015, auth.go: bearerToken/lookupAPIToken/requireCookieUser); changing
                       the password revokes every token and every other session
  trello_compat.go     Trello-compatible API for chgksuite (token-authed via key+token)
  invite.go            invite minting (subcommand)
  admin.go             /admin + /admin/create_users (gated on XY_ADMIN_USER, default "pecheny"); the create-users body is kit.AdminCreateUsers, xy wraps its chrome
  export.go            POST /api/export/{docx,pdf} — one 4s source + images, exported two ways, both fully
                       in-process (chgk/docx, chgk/typstdoc), images included; no Python. The PDF goes through
                       the shared typst (wasm) pool (typst.go), so it too writes nothing anywhere
  exportpack.go        POST /api/export/pack — the export modal's request: one 4s source, several formats
                       (4s/docx/pdf/pdf_mobile/handouts) rendered by composing the above + handout.SplitFit,
                       returned as the bare file when one was asked for or a zip when more. Images ride along
                       only for the .4s (docx/pdf embed their own); split-fit's PDFs land under раздатки/
  import4s.go          POST /api/import/parse — .4s/.zip/.docx → 4s source + images (chgk/chgkimport),
                       parsed in memory, nothing persisted; the client encrypts the result into a new list.
                       POST /api/import/text — the same pipeline without the file: one card's plain text
                       (a question pasted as prose) → 4s, behind the card editor's →.4s button
  handouts.go          POST /api/handouts/{pdf,split_fit} — fully in-process (chgk/handout + typst as a wasm module, see typst.go). No Python, no typst binary, nothing written to disk. Normalize CRLF→LF first (browsers send multipart text as CRLF, which broke the .hndt "---" splitter)
  typst.go             the shared typst (wasm) pool: built once, warmed at boot, injectable so handler tests stub it. XY_WASM_CACHE must be persistent (~15s cold compile vs ~0.6s cached)
  sessions.go          test sessions (ADR-0004): a board-level entity, not a card in a list.
                       CRUD + the session's own лента (a comment may hang off a card, off a
                       session, or off both). A Playing — card_sessions, this question was
                       played at that test — is what «Видели» reads; a label ASSIGNMENT then
                       carries an optional session, so «взяли» is ONE board label composed onto
                       a playing rather than one label per sitting
  reap.go              the tombstone reaper (ADR-0002): every delete is a 14-day tombstone; an hourly
                       loop (+ `xy-server gc` on demand) hard-deletes expired ones, destroys their blobs,
                       and sweeps orphaned blob files
  staging.go           handout image staging: /api/handouts/{stage,heartbeat,DELETE stage} — client uploads referenced images once on modal open; pdf/split_fit reuse them via a session id (reaped after ~1min of no heartbeat) instead of re-uploading each generate. Staged images live in memory only, never on disk
  multipart.go         readMultipart: in-memory multipart parsing for every endpoint that receives plaintext (export/handouts/staging/import). ParseMultipartForm spills parts over its budget into an unmanaged temp file — plaintext on disk is exactly what xy must not do. (attachments.go still uses it: those uploads are ciphertext.)
  debug.go             [timing] logs on export/handout endpoints, gated by XY_DEBUG_TIMING
internal/chgk/         Go port of chgksuite's core (xy no longer shells out to Python for docx/handouts)
  fsource/             the "4s" format, both ways: parse.go = parse_4s (oracle-tested vs
                       chgksuite --debug), compose.go = compose_4s (structure → 4s text).
                       testdata/parity.json is the Go/TS corpus: every fixture line split
                       at its marker, (img …) references, one .hndt the browser writes and
                       Go parses; parity_test.go is the oracle (regenerate with
                       `go test ./internal/chgk/fsource -run TestParity -update`),
                       jstest/fsource_parity.test.js the other reader. markerMapping is the
                       one marker table: `go generate` writes web/ts/markers_gen.ts
  typo/                typotools.py: the typography pass (quotes/dashes/stress accents/
                       %-decoding) + URL-aware underscore escaping
  typoedit/            the typography pass: typo (quotes/dashes/%-decoding — every knob but
                       accents, which have their own button) + inline's nbsp/nbhyphen gluing,
                       applied to 4s SOURCE rather than to a field's value.
                       Every line is split at its marker first (fsource.SplitMarker) — a pass
                       let loose on raw 4s reads a list item's leading "-" as a stray hyphen
                       and turns it into an em dash, eating the list.
                       NO PRODUCTION CALLER: the button runs the TypeScript port (web/ts/typo.ts),
                       because question text must not be posted to a server that may not see it.
                       This package is the parity ORACLE — both suites read testdata/pass_cases.json
  docxread/            .docx → plain text — a hand-rolled python-docx (zip/OPC, runs,
                       hyperlinks, numbering, tables, image extraction, in memory, no fs)
  textparse/           parser.py's parsers: plain text → structure. Literal ports, quirks
                       included (see the comments). parse.go is ChgkParser (chgk and brain);
                       si.go + troika.go are SiParser and TroikaParser, which read the
                       document's outline through the "$$HN$$" markers docxread writes for
                       them; db.go is parser_db's PLY lexer over db.chgk.info's own text
                       export (a .txt that opens «Чемпионат:»). All byte-parity tested
                       against chgksuite's corpus canons
  textenc/             a .txt package whose encoding nobody recorded: UTF-8, or whichever of
                       CP1251/KOI8-R/CP866/ISO8859-5 spells the most Russian-looking letters
                       (chgksuite asks chardet; Go has none)
  markdown/            `compose markdown|redditmd`: a package as Markdown, or as the
                       Reddit dialect where the answer is a >! … !< spoiler
  dbtext/              `compose base`: the plain text db.chgk.info takes as a submission —
                       the other side of textparse/db.go. #DATE is read by a parser that
                       covers the shapes packages use, not by chgksuite's dateparser
  openquiz/            `compose openquiz`: openquiz.me's JSON, one object per question
  imghost/             what those three need and the .docx/.pdf ones do not: a picture as
                       a URL. Imgur, sharing chgksuite's own ~/.chgksuite/image_cache.json
  chgkimport/          the import entry point: .docx/.4s/.zip → 4s source + its images.
                       Byte-parity with chgksuite's `parse` on all 12 chgk .docx fixtures
  handout/             .hndt → .typ (byte-exact vs chgksuite) → PDF via typst; embeds the typst template + Noto Sans.
                       typesetter.go: the Typesetter interface. The server uses the wasm one (typstwasm), so
                       nothing is written anywhere. CLITypesetter drives the typst binary and is kept ONLY as the
                       oracle the wasm path is checked against (wasm_parity_test.go: the fitted row counts must
                       match, since split_fit binary-searches them).
                       splitfit.go: `handouts split_fit` port — per-block binary-search row fit using typst's own pagination (typst query page count, not pypdf), per-question + all-q PDFs, pdfcpu compress; ~12× faster, row counts match chgksuite. The image-shrink refinement needs a Typesetter that also measures (handout.Measurer): the typst binary answers with a query, the wasm pool cannot yet, and under it an image block simply keeps its size
  tg/                  `compose telegram`: a package posted to a channel, one rich message per
                       question (<p>/<details>/<footer>/<img>), the copy Telegram makes in the
                       discussion group carrying the polls, a pinned navigation post at the end.
                       The export runs its own in-process bot (dopecore/tgbot, like the login
                       bots) for the two things a token alone cannot do: hearing the person who
                       started it, and seeing a post reach the discussion group. Oracle-tested
                       call-for-call against chgksuite (scripts/gen_tg_oracle.py)
  typstwasm/           typst linked in as a library, compiled to wasm32-wasip1, run under wazero with its
                       World (= typst's filesystem abstraction) served from memory. Removes the last place xy
                       had to hand decrypted questions to a filesystem. A pool of instances, since split_fit
                       fits blocks in parallel; fonts parsed once, images once per generation.
                       ~8× faster per probe than spawning the CLI (1.4ms vs 11.3ms).
                       typst.wasm is //go:embed-ed but NOT in git (30 MB): `just build-wasm` compiles
                       typst-wasm/ (Rust) into it — once per clone, then only on a typst bump. Every Go
                       recipe (build/dev/test) depends on a guard that says so if the file is missing.
  docx/                parsed structure → .docx (OOXML), reusing chgksuite's template.docx; byte-parity tested (document.xml body + rels: spacing, run boundaries, hyperlinks) vs chgksuite, for every `compose docx` switch (docx.Options: spoilers, screen mode, noanswers/noparagraph/only_question_number). xy passes Options{} — the switches are the CLI's for now; regenerate the oracles with scripts/gen_docx_oracles.sh.
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
    find.ts            the matching rules behind search and find-and-replace, all pure:
                       folding (search sees through the accents/NBSP/«ёлочки» the typography
                       pass wrote), literal matching for a replacement (space matches NBSP,
                       hyphen matches NBHY, case toggle), the guard that makes a marker
                       prefix and a (hidden-comment xy-version: …) head unmatchable, and the
                       snippet a hit is shown by
    searchindex.ts     the Search Index (ADR-0008): one PLAINTEXT record per board in
                       IndexedDB ("searchindex" store) with each card's 4s, its alias and the
                       comments on it. Written only where a data key is already held —
                       board.ts on every render, and «Прогрев поиска» (☰ on the index page),
                       which downloads every board this device can unlock so coverage stops
                       depending on which boards were opened. Dropped with the key
                       (forgetDK / board delete). search() is pure and jstest-covered
    store.ts           offline IndexedDB layer: snapshot/timeline/attachment mirror,
                       mutation outbox, temp-id↔real-id map (DB "xy-offline")
    sync.ts            offline engine: mutate()/flush() outbox replay with negative
                       temp-id remapping, snapshot apply, pending-timeline synthesis,
                       online/offline status events (PWA resync)
    sw.ts              service worker — app-shell caching (served at root, scope '/')
    overlaystack.ts    one owner for every overlay's dismissal. An overlay registers on open;
                       the stack pushes a history entry, so Android's back button closes it
                       instead of the app (it has no Escape key). Back, Escape and the
                       ✕/↩️ buttons all funnel through the same popstate, so they cannot
                       drift apart. It shows/hides nothing — each overlay hands in the close
                       function it already had. `confirm` gates a dismissal (the card's
                       unsaved-changes prompt); `replace` swaps the top overlay for a
                       hand-off (list preview → card) rather than a dismissal. Transient
                       popups (⋯ menu, label picker, 🔔 panel) stay OFF the stack and claim
                       Escape in the CAPTURE phase, so they close before the card does
    modal.ts           the one lifecycle for a plain `modal` block: modal(stem) finds
                       <stem>Overlay/Close/Cancel/Message, owns `hidden`, the stack
                       registration, the close buttons and the backdrop; open({onClose,
                       confirm}) / close() / message(). Every plain modal on every page
                       (board, its kernels, profile, index) is one of these; the card
                       docoverlay, the list preview, the lightbox and the card's dirty
                       prompt are not
    rank.ts            fractional indexing (LexoRank-style keyBetween)
    app.ts             shared fetch/DOM helpers (byId, errMsg, el, the sync badge), derived
                       titles, offline-tolerant requireLogin
    diff.ts            word-level token diff for desc_edit timeline highlighting
    index.ts           board list + create-board (passphrase) flow; offline board-list cache;
                       the search box over both grids — the top grid filters to boards the
                       query can NAME (names are plaintext), the grid below shows the cards
                       it can quote, so a board appears in exactly one place
    board.ts           the board page: boot (unlock.ts), the render loop (lists,
                       cards, the group numbering, the Search Index write), list and
                       card drag (dragrank.ts), the card detail + timeline wiring
                       (carddetail.ts, timeline.ts, attachments.ts), the list preview
                       overlay (drawn by preview.ts), the wiring of cardlabels.ts,
                       bell.ts and transfer.ts, and the Board seam + panel registration
                       (panels.ts): one
                       registerPanel(...) call lists every ☰ and ⋯ entry in menu order,
                       and both menus render from it. Board-level actions that are not
                       panels (rename/delete board, forget password, add/rename/delete
                       list, preview) are registered inline. Display sizes
                       (users.sizes, edited on /profile) arrive in the snapshot and become
                       CSS vars on <html>: --kanban-max-w, --klist-w, --kcard-lines (a
                       line clamp — don't reintroduce a char cap in cardBody). A card
                       previews alias → (question text | answer): the alias is the card's
                       own encrypted column (cards.alias_enc, NOT a 4s marker — parity),
                       its input sits below the view panels and PATCHes itself a second
                       after typing stops; the fallback is users.card_title. The card
                       editor's tools row: ударение types U+0301 into the field the caret
                       was last in (the row swallows mousedown so the field keeps focus);
                       типограф (typo.ts, in the browser, every version) and →.4s rewrite
                       the whole draft; every edit goes through execCommand("insertText")
                       so Ctrl-Z survives. ← / → walk the open card's list (its whole
                       group when in one); leaving a dirty card by any route raises the
                       Save / Discard prompt. Автор/Источник inputs autocomplete via
                       suggestWrap (<datalist> never opens on iOS Safari)
    carddetail.ts      the card kernel: open/close, the draft (carddraft.ts) and its
                       dirty gate, the three views (Просмотр / Поля / Текст) and the tools
                       row, versions, alias, the move/copy dialog (over transfer.ts), read
                       tracking. Its nodes come in as
                       a CardDetailUI record from board.ts (document remains for the
                       clipboard, execCommand and the page-wide keydown listeners), so it
                       runs under the DOM shim (carddetail_ui.test.js). Its unsaved-changes
                       prompt (dirtyOverlay) toggles hidden itself: it opens from inside the
                       stack's confirm gate, which is what modal.ts would register on
    preview.ts         xy's one screen rendering of a question: renderPreviewCard(card,
                       number, imgMap, screen, edit?) as the docx export would set it, over
                       renderRich (chgk's inline runs → DOM, print/screen mode); the list
                       preview, the card's Просмотр and the import preview draw through it
                       (preview.test.js)
    cardlabels.ts      the open card's «Метки», «Тесты» and «Видели» (ADR-0004): two
                       pickers over one filtered popup, the create-label form mounted at
                       its foot, every write as the card's whole set through the verbs.
                       «Видели» subtracts the tour's common testers; «Показать всех
                       тестеров» un-hides them dimmed for as long as the card is open
                       (cardlabels.test.js over fakeBoard)
    bell.ts            the 🔔: the badge and the panel of recent other-authored activity,
                       wording each event with timeline.eventVerb (one verbs map)
    timeline.ts        the card's лента kernel: load/render, comments (drafts with images,
                       replies, выписки), edit diffs, the expanded feed, filters and view
                       prefs; nodes as a TimelineUI record
    attachments.ts     the attachment kernel: the per-card list cache and the decrypted
                       bytes/URL LRU keyed id:rev, upload (opt-in WebP), replace, delete,
                       paste-to-attach, download with the offline mirror, the lightbox;
                       the editor's nodes come in as an AttachmentsUI record
                       (attachments.test.js drives it on the DOM shim)
    panels.ts          the panel registry and the Board seam. Board = the live state,
                       the read helpers (cardsOf, listsInGroup, sessionName…), the four
                       mutation verbs, render, setStatus, reload. registerPanel(...) takes
                       {id, menu: "board"|"list", icon, label | label(scope), title?,
                       offered?(scope), open(scope)}; boardMenu() / listMenu(scope) return
                       the two menus as data; listScope(board, list) is what a per-list
                       panel works on (the list or its whole group, cards concatenated in
                       board order, their display numbers as one run across the group,
                       the title); listNumbers(board, list) is one list's slice of that
                       run — the kanban column and the card editor read it, so a panel
                       never re-derives numbering. createPanelShell is one generic modal
                       (board.dopeui's panelOverlay) for panels that build their body with
                       el() — a new such panel touches no .dopeui, vocab or Go
    rewrites.ts        board-wide description rewrites: collect(transform) / apply(changes)
                       — each changed card patched with a desc_edit entry, so the rewrite
                       is auditable — behind «Исправить оформление Trello», «Типографить
                       всю доску» (with the stress-mark review modal) and the legacy
                       Version conversion on load. replace.ts borrows apply
    replace.ts         «Найти и заменить»: one literal replacement across the board, one
                       list or one group; every occurrence ticked with its context (100 per
                       page), markers and version heads unmatchable (find.ts), a card whose
                       text moved between plan and run is skipped and named
    listsmanage.ts     «Управление списками»: reorder lists and groups by drag or by
                       position, link consecutive lists into a list_of_lists; unitsOf folds
                       lists into orderable units and applyUnitOrder writes an order — the
                       board's column drag uses both
    transfer.ts        moving or copying a Card out of its List: a re-rank or a duplicate on
                       the board, a client-side re-encryption onto another (description,
                       alias, handout settings, comments, attachments; labels reconciled by
                       name+colour, Playings by the Session's key). createTransfer(deps) →
                       loadMoveBoard / moveBoardOptions / transferCard(card, list, ctx,
                       remove, rank?); the card editor, «Массовое действие» and
                       «Переместить список…» all go through it (transfer.test.js, real keys)
    movelist.ts        «Переместить список…»: within the board a move is a plain re-rank;
                       everything else goes out as a Bundle and back through applyBundle,
                       so a travelling list carries what an exported one does
    importpack.ts      «Импорт»: the board's one file picker — sniffBundle tells an xy
                       archive from a question package, so the reader picks a file and
                       not a format. A package goes .4s/.zip/.docx to /api/import/parse, the returned 4s
                       into a new list (or a group of lists, one per «## …» tour), each
                       (img …) attached to the card that references it; a .docx first opens
                       the verification screen (editable 4s left, live preview right)
    export.ts          «Экспорт»: exportSource (the cards' 4s, versions folded) + the
                       referenced images to /api/export/pack for .docx/.pdf/.pdf для
                       телефона/раздатки; a bare .4s with no images is written in the
                       browser, the one export that works offline
    handouts.ts        «Генерация раздаток»: hndtOf (hndt.ts) → editable .hndt →
                       /api/handouts/{pdf,split_fit}; images staged once per open
                       (handoutsession.ts); per-question layout settings persisted to
                       handout_meta on close
    masspanel.ts       «Массовое действие»: the mode, the bar, the tick state the render
                       loop paints, the one dialog with its pickers, and the per-card writes
                       (rules in massaction.ts)
    labelsedit.ts      «Метки»: the label editor (rename/recolour/delete/create; Готово
                       commits the lot); sortLabels is the board's one ordering of labels
    testerlist.ts      «Список тестеров»: who tested a tour and how much of it; the tour's
                       Declaration (tour_testers) or, undeclared, the custom — everyone who
                       saw more than half (tourPicked, shared with the card's «кроме общих
                       тестеров» line). Renders into the panel shell
    authorcount.ts     «Счётчик авторов»: countAuthors (pure) and its panel in the shell
    pwa.ts             PWA boot on every page: manifest/install <head> tags + sw
                       registration + zoom lockdown (theme boot + ☰ menu come from
                       the kit's shared menu module)
    timer.ts           floating ЧГК play timer (⏰ in the board header): question
                       minute + 10s answer countdown, WebAudio bell cues. createTimer is
                       the kernel (presets, phase machine, cue schedule) over an injected
                       clock, bell and view — jstest drives it on a fake clock and a
                       recording bell; mountTimer is the page adapter (the box, WebAudio,
                       the toggle)
    chgk.ts            client-side 4s parser for card previews (display-only,
                       never rewrites the source): blocks, numbering, the inline
                       tokenizer and renderer, structured fields, copy targets, the
                       version separator rule (versionLineName); its marker table is
                       markers_gen.ts, and imgRefs (the (img …) references of a text,
                       through the inline tokenizer's bracket matching) is the one
                       image finder — imageRefs(cards) is its set over cards; export,
                       handouts, import and the preview call one or the other
    versions.ts        the Version algebra (ADR-0007): split/count/body/name, add/
                       remove/promote, composeVersions (the export's one question with
                       every wording page-broken) and the legacy (PAGEBREAK) conversion
    hndt.ts            раздатки's .hndt side: generateHndt (the 4s2hndt port),
                       handoutForCard, parseHndtMetaByQuestion — one document of it is in
                       the Go/TS parity corpus
    markers_gen.ts     GENERATED from fsource's markerMapping (`go generate
                       ./internal/chgk/fsource`, guarded by generate-check): the 4s line
                       markers and their element types, longest first. Go and TS cannot
                       drift on what a marker is
    typo.ts            the typography pass, ported from internal/chgk/{typo,typoedit}:
                       quotes/dashes/%-decoding + nbsp gluing over 4s source, per version.
                       In the browser so no question text is posted and it works offline;
                       the Go packages stay as the oracle and both suites read
                       internal/chgk/typoedit/testdata/pass_cases.json. One known
                       divergence (the nbhyphen bounds) is documented at the top of the file
    import.ts          /import — a new board from an archive or from Trello (implicit
                       OAuth, server proxy /api/import/trello/proxy, comments past the
                       1000-action cap, attachments). trelloBundle is the Trello producer;
                       every write is applyBundle's. The pure Trello-card→xy-card rules
                       live in trellomodel.ts (jstest-covered, no DOM)
    bundle.ts          the Bundle's shape (ADR-0013), all pure: what board.json holds,
                       attachment paths inside the zip, the validation an untrusted file
                       passes before an import touches the server, and the two the pickers
                       run on — unitsOf (lists folded into ticks; a group is one, always
                       whole) and sliceBundle (a selection cut down to what its cards
                       reach: their labels, the sessions they were played at, the
                       declarations of those tours, their лента and attachments)
    zip.ts             a minimal zip writer/reader for Bundles: store/deflate via the
                       native CompressionStream, UTF-8 names, no zip64 (jstest-covered)
    bundleapply.ts     the ONE write path a Transfer takes (ADR-0014): applyBundle(bundle,
                       target, bytesOf) — target is a board created for it (verbatim ranks,
                       a row per label/session) or an AppendState (append after the target's
                       last list; labels fold on name+colour, sessions on their key per
                       ADR-0003 with an origin stamp). The List is the unit of atomicity:
                       a unit that fails deletes its own lists and the ones before it stay
    bundleexport.ts    «Экспорт (.zip)…» (☰) and the live→Bundle producer: buildBundle
                       decrypts what the ticked lists reach; zipBundle packs board.json +
                       attachments; tickList is the picker both bundle panels show.
                       Attachment bytes come back lazily so a move never holds a board's
                       раздатки on the heap
    bundleimport.ts    reading a Bundle zip, and the target that needs no open board:
                       createBoardFromBundle (quota pre-check, fresh key, applyBundle,
                       and a failure deletes the board it just made)
    bundleimportpanel.ts a Bundle appended to the OPEN board — tick its lists, warn on
                       a title already here, report which units landed. No menu entry of
                       its own: «Импорт» (importpack.ts) sniffs the picked file and hands
                       an xy archive here. On the board page because only it holds the key
    sessions.ts        the test-session kernel, all pure: parse/serialize meta_enc (folding
                       every older shape forward), the derived session name, dd.mm.yyyy +
                       24h parsing (native date/time inputs render in the BROWSER's locale,
                       not the page's), zone offsets and the IANA list off Intl, and the
                       invite line — «20 июля, 19:00 (Берлин) / 21:00 (Москва)» — computed
                       from wall clock + zone, so DST applies for the session's own date;
                       and the tester lists of the test cards the sessions grew out of
                       (parseTestCard, testersToText…, the Tester type)
    sessionspanel.ts   the «🧪 Тесты» panel that replaced the тест-список: one row per
                       session, the session form (towns/timezone/tester autocompletes), the
                       invite and tester-summary copies, and the session's лента. The form
                       has no Отмена: leaving it by ANY route saves (the overlay stack's
                       confirm gate), and only a failed save keeps you on it; each row's ▶
                       starts test mode for that session on this device
    testmode.ts        test mode (ADR-0012), the kernel: one localStorage slot per device
                       (board+session+last-activity+do-not-remark), an idle hour ends it by
                       wall clock, and the dwell watcher marks a card open for a minute —
                       all pure over an injected clock/store. board.ts wires the dwell, the
                       topbar badge and the born-tagged comments; pwa.ts feeds the idle
                       clock from every page
    colorpick.ts       the label colour control: a swatch button opening a fixed palette
                       plus a hex field, replacing input[type=color] (which hands the
                       choice to a full-screen OS sheet on Android). The kit's
                       `colorfield` primitive is gone with it. The palette is uchu's
                       pastel set (uchu.style), shade 5 of all eight hues — ONE rung
                       across the set, which is what an OKLCH palette buys. textOn then
                       picks uchu's yin or yang per fill by WCAG contrast, and paintLabels
                       applies it: a label's colour is the user's, so the ink follows it.
                       The hardcoded white on .label-pick is what forced the previous
                       palette to be uniformly dark — don't reintroduce it
    popup.ts           anchorPopup: a transient popup mounted on <body> at a fixed,
                       viewport-clamped spot, with the outside-click / Escape / scroll /
                       resize dismissals. Shared by the list ⋯ menu and the colour palette
                       — both open from inside a container that would clip them. A popup
                       body-mounted this way is NOT inside the anchor of whatever opened
                       it, so filteredPopup treats an open .menu-fixed as stacked above
                       itself; without that, picking a colour dismissed the add-label
                       popup that contained the form
    towns.ts           ЧГК's towns joined to their IANA timezone (data only, generated by
                       scripts/fetch_towns.py from api.rating.chgk.net + GeoNames; bundled
                       because the CSP forbids reaching either at runtime)
    people.ts          the person directory: tester names this device has seen, gathered
                       from every board whose key it holds. Plaintext in localStorage on
                       purpose — the same device caches the DKs — and purged per board when
                       its password is forgotten
    labelfilter.ts     «Фильтр по меткам»: pick labels + все/любая/ни одной and every
                       list draws only what matches. A way of looking — it reaches the
                       drawing and nothing else (numbering, exports and transfer still
                       see every card), writes nothing, and dies on reload. Under it
                       drag-to-reorder is off, because commitCardMove reads the rank off
                       the VISIBLE neighbours; a cross-list drop appends instead
    gallery.ts         /gallery (dev only): every primitive a panel is built from, on
                       one page from fixtures — the sheet the design-review skill judges
                       a new surface against, and where a skin change is looked at
    boardinvites.ts    the invite-link half of the «Участники» modal (ADR-0017): the
                       Заявки queue and the Ссылки list, plus the chip mint form. Owner
                       only; a decision redraws the roster through onChange, which also
                       repaints the ☰ row's waiting count
    firstrun.ts        the two questions every account answers once (timezone, default
                       author), asked on whichever page is opened first
    wordlist.ts        EFF diceware list for generated passphrases (data only)
    profile.ts         /profile: username set-once, logout, and five dialogs — change
                       password, board sizes (three sliders + a to-scale pseudo-board
                       preview, wireframe bars for text; defaults 1512px / 280px / 3
                       lines, max slider position = unlimited/null; debounced POST
                       /api/auth/sizes), default author (POST /api/auth/default-author),
                       card title (POST /api/auth/card-title — question text vs answer),
                       лента (POST /api/auth/feed-default — which kind of timeline
                       entry an opened card starts on).
                       Shared defaults/ranges/sanitize/apply live in app.ts (xySizes)
                       so this write path and board.ts's read path agree
    tokens.ts          /profile/tokens — create/revoke API tokens for the Trello API
    join.ts            /join/<code> — what an invite link opens: the board's name,
                       why the link does or does not work, and one button (ADR-0017)
web/assets/            //go:embed static + ui (package assets)
  ui/                  the 7 app pages as .dopeui (index, board, login, profile,
                       tokens, import, join) — compiled to HTML by internal/ui at server
                       startup (per-request in dev disk mode; /login recompiles per
                       request only for ?next=/join/<code>)
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
  plaintext board names — and the Search Index, which is deliberately plaintext
  (ADR-0008: the raw DK already sits in `xy-keys`, so plaintext beside it adds no
  exposure; it is purged whenever the key is).
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
just dev            # server (assets hot-read from disk; polls telegram if XY_BOT_TOKEN is set)
just cli            # xy-cli → ~/.local/bin (the board from the shell; .claude/skills/xy-cli)
just invite 7       # mint a registration invite
# bootstrap a password account (registration is otherwise telegram-only):
printf '<password>' | XY_DB=… xy-server adduser <username>   # password via stdin
just test           # go test + deno frontend tests
# XY_TYPST_TEST_BIN=/path/to/typst  → also runs the typst-CLI parity tests (the
#   oracle the in-process wasm typst is checked against). typst is NOT needed to
#   run xy — only to run those tests.
just check          # this module: fmt + vet + tidy-check + test
just pre-commit     # the whole repo (the root justfile), incl. the kit's gate and
                    #   class-check, which no single module can run for itself
just deploy-staging # a branch may go to xytest.pecheny.me for a live test
just deploy         # prod: from `main` ONLY, merged AND pushed to origin first —
                    #   never from a branch (a branch deploy once undid another's fix)
```
Server listens on `$PORT` (default 9673); DB at `$XY_DB` (default xy.db).
Config via `.env` (see `.env.example`). Telegram register/login needs
`XY_BOT_TOKEN`: the server IS the bot (internal/server/bot.go), and an instance
without a token does not poll and does not offer telegram login.

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

**Search** is client-side and local-only (ADR-0008, `searchindex.ts`); server-side
encrypted search stays out of scope.

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
