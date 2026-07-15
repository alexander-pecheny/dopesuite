# XY — Implementation Plan

A Trello-style board app for ЧГК (trivia) editing, built by reusing `../dope`
(Go + SQLite backend, vanilla-JS frontend, shared design system). The defining
difference from dope: **user-entered data is encrypted client-side** with a
per-board passphrase (board names excepted — plaintext; see the trust model), and
the app works **offline as a PWA**.

This plan reflects four decisions made up front:

1. **Milestone 1 = core encrypted boards.** Auth, boards/lists/cards/labels/
   timeline, client-side encryption, drag-reorder, test lists. Offline/PWA,
   encrypted search, Trello-API compatibility, and chgksuite import/export are
   sketched here but built in later phases.
2. **Key model = passphrase wraps a random board key.** Each board has a random
   data key (DK); the passphrase derives a KEK (Argon2id) that only wraps/unwraps
   DK. Changing the passphrase re-wraps DK without re-encrypting board data;
   sharing means sharing the passphrase out-of-band.
3. ~~**Reuse = copy into a fresh repo.**~~ **Superseded by the monorepo merge.**
   xy was built as a separate repo that copied and adapted dope packages rather
   than refactoring dope. xy and dope now live in one repo (dopesuite), so those
   copies are simply duplication — debt, not policy. It is being paid down: a
   shared `dopecore` module (webassets, sqlitex, session, authcred) is being
   extracted, and both apps move onto it. Do not add new copies.
4. **Telegram login = reuse dope's bot bridge** (separate bot binary + shared-
   secret endpoints), with its own bot token.

---

## 1. Architecture overview

> **Note (2026-06-19):** `../dope` was refactored from a flat `package main` into
> seven semantic groups under `dope/dope/` (cmd, server, web, domain, storage,
> export, platform — see `../dope/ARCHITECTURE.md`). xy does **not** import dope;
> it is its own `module xy` that reuses dope's *patterns* and copies the needed
> frontend assets. xy is much smaller than dope, so it uses a flatter layout
> (not dope's seven groups) while keeping the same idioms (write-tx discipline,
> `BuildDSN` pragmas, `//go:embed static`, `window`-globals frontend).

```
xy/                         module root (go.mod: module "xy")
  cmd/xy-server/            thin main() → server.Main()
  cmd/telegram-bot/         login-code bot (pattern from dope, own token)
  internal/server/          HTTP server, routing, handlers, auth
    main.go                 mux wiring, gzip, ETag, dev disk-read
    db.go                   open DB, DSN/WAL pragmas, migration runner, write-tx
    auth.go                 sessions, login/register, password, telegram bridge
    boards.go               boards/members/keymeta endpoints
    lists_cards.go          lists/cards/labels/timeline endpoints
    attachments.go          encrypted attachment endpoints
  internal/session/         cookie + session.User (ported from dope/platform/session)
  internal/blobstore/       encrypted attachment storage (new)
  web/assets/               //go:embed static  (package assets)
    static/
      crypto.js             WebCrypto + scrypt (noble) envelope layer (new)
      board.js              kanban board UI (new)
      card.js               card detail + timeline UI (new)
      styles.css            design system (ported from dope, trimmed)
      menu.js / login.*     chrome + auth UI (ported)
      vendor/noble-hashes.js  pinned, SRI'd single JS dependency
  jstest/                   node frontend tests (crypto round-trips, ranks)
  justfile                  (ported patterns; deploy is the monorepo's ../deploy.py)
```

**Trust model.** The server is treated as honest-but-curious: it stores and
serves ciphertext and the *structural metadata* needed to order, sync, and
authorize (entity IDs, parent IDs, positions, timestamps, types, member ACLs),
plus **board names in plaintext** (see below). It can never read list/card
descriptions, comments, label text/colors, or attachment bytes. Server-side ACL
(board membership) and the passphrase are defense-in-depth: membership gates who
can *fetch* a board's ciphertext, the passphrase gates who can *decrypt* it.

**Explicit metadata leakage (accepted).** The server learns board/list/card
structure, item counts, positions, timestamps, authorship, label↔card
associations (as opaque IDs), attachment sizes/mime, and **board names**. It does
not learn any other content. Board names were de-encrypted deliberately: keeping
them encrypted meant the board list couldn't be read without unlocking every
board (data key per board), which cost more UX than the names were worth. Every
other field stays encrypted. This is the pragmatic tradeoff that keeps relational
ordering, sync, and realtime simple. Documented so it's a conscious choice, not
an accident. (Per-board `schema_version`: 1 = name still encrypted in `name_enc`;
2 = plaintext `name`. Legacy boards are backfilled lazily by clients holding the
DK; see `migrateV10` and `POST /api/boards/{id}/migrate-name`.)

**XSS = total client compromise.** Because all crypto is client-side, an XSS
hole defeats it entirely. Therefore: strict `Content-Security-Policy` (no inline
scripts, no eval, no `wasm-unsafe-eval`), no third-party script origins, the one
JS dependency (`@noble/hashes`) self-hosted and pinned with Subresource
Integrity, and HTML-escaping discipline in all render paths. This is a
first-class requirement, not a polish item.

**No WASM anywhere.** The crypto stack must run with WebAssembly disabled (e.g.
iOS Lockdown Mode). That rules out WASM Argon2 builds; the KDF is pure JS
(`@noble/hashes` scrypt) and the cipher is native WebCrypto.

---

## 2. Cryptography design

**Primitives.**
- KDF: **scrypt** (passphrase → 32-byte KEK) via `@noble/hashes/scrypt` —
  pure JS, audited, zero-dependency, **no WASM** (runs under iOS Lockdown Mode).
  Memory-hard, so it resists offline GPU/ASIC brute-force of the passphrase if
  the wrapped-key blob ever leaks. Params tuned for a tolerable one-time unlock
  on mobile: start `N=2^15 (32768), r=8, p=1` (~32 MiB) and measure; the KDF id +
  params are stored per board so they can be raised — or swapped to another KDF —
  later without breaking existing boards.
- Symmetric cipher: **AES-256-GCM** via native `crypto.subtle` (random 12-byte
  nonce per encryption, 16-byte tag).

**Per-board key lifecycle.**
1. On board create, client generates random DK (32 bytes) and random KDF salt.
2. `KEK = scrypt(passphrase, salt, params)`.
3. `wrapped_key = AES-GCM(KEK, DK)`; a fixed `verify_token =
   AES-GCM(DK, "xy-verify-v1")` lets the client confirm a passphrase on unlock.
4. Server stores `{salt, params, wrapped_key, verify_token}` in plaintext
   (none reveal anything without the passphrase).
5. To unlock: client fetches those, derives KEK, unwraps DK, verifies token,
   caches DK in **IndexedDB** (survives reloads; see caching note).
6. Passphrase change: derive new KEK, re-wrap the *same* DK. No data re-encrypt.

**Encryption envelope (one canonical format).** Every encrypted field/blob is:
`magic("xy1") | alg(1) | nonce(12) | ciphertext+tag`, stored as SQLite `BLOB`
(or base64 in JSON over the wire). One `crypto.js` encode/decode pair is the
only place this format lives; covered by node round-trip tests.

**Key caching.** DK (not the passphrase) is cached per board in IndexedDB,
optionally behind a session-scoped flag for "remember on this device". Neither
IndexedDB nor cookies protect against XSS — the CSP above is the real defense.
Provide an explicit "lock board" / "forget passphrase" action that purges DK.

**Cross-board copy/move** is inherently client-side: client decrypts with the
source DK and re-encrypts with the target DK, then issues structural API calls.
Labels are reconciled client-side (match by decrypted name+color; create missing
ones at the target). See §6.

---

## 3. Data model (SQLite)

Ported auth tables (unchanged from dope): `users`, `sessions`, `invites`,
`telegram_login_codes`, `schema_versions`.

New tables (content columns suffixed `_enc` are encryption envelopes; everything
else is plaintext structural metadata):

- `boards(id, owner_user_id, name, name_enc, schema_version, kdf_salt, kdf_params,
  wrapped_key, verify_token, created_at, updated_at)` — `name` is plaintext
  (schema_version 2); `name_enc` is the legacy encrypted name kept for
  not-yet-migrated (schema_version 1) boards until a later retirement migration
  drops it.
- `board_members(board_id, user_id, role)` — role ∈ {owner, editor}. ACL only.
- `lists(id, board_id, type['normal'|'test'], title_enc, rank, created_at,
  updated_at, deleted_at)`
- `cards(id, board_id, list_id, kind['normal'|'test'], description_enc, rank,
  created_at, updated_at, deleted_at)` — no title column; titles are derived
  client-side from the decrypted description (first words, then fade), like
  dope's table cells.
- `labels(id, board_id, name_enc, color_enc, kind['normal'|'test_taken'|
  'test_missed'], created_at)`
- `card_labels(card_id, label_id)` — plaintext association.
- `timeline_events(id, board_id, card_id, type['comment'|'desc_edit'|
  'label_add'|'label_remove'|'attach_add'|'attach_remove'|'attach_replace'],
  author_user_id, created_at, payload_enc)` — `payload_enc` holds the type-
  specific encrypted JSON (comment text; `{before,after}` for desc diffs; etc.).
- `attachments(id, board_id, card_id, filename_enc, mime, size, lossless,
  blob_ref, created_at, deleted_at)` — bytes live in the blob store, encrypted.
- `board_player_map(board_id, payload_enc)` — encrypted rating.chgk.info
  player-id → name correspondence used by test cards (single blob for M1).

**Ordering.** Use fractional/lexicographic ranks (a string "rank" à la LexoRank,
or float midpoints) so a drag updates only the moved item's `rank` — important
for cheap reorders now and conflict-tolerant offline merges later.

**Soft deletes** (`deleted_at`) keep sync/undo simple and play well with the
later offline outbox.

Reuse dope's **migration runner** (`schema_versions` + ordered SQL steps) and
its **write-tx discipline** (single write lock, connection acquired off-lock,
bounded tx) from `store`/`core`.

---

## 4. HTTP API (API-first)

Everything doable in the UI is a JSON endpoint; the UI is just a client. Request/
response content fields carry **ciphertext envelopes** (base64); the server
validates structure and ACLs only, never content.

- Auth (ported): `POST /api/auth/login/{start,password,code}`,
  `/api/auth/register/*`, `GET /api/auth/me`, `POST /api/auth/logout`,
  `PUT /api/auth/{username,password}`.
- Boards: `GET/POST /api/boards`, `GET/PATCH/DELETE /api/boards/{id}`,
  `GET /api/boards/{id}/keymeta` (salt/params/wrapped_key/verify_token),
  `PUT /api/boards/{id}/keymeta` (passphrase change = re-wrap).
- Members: `GET/POST/DELETE /api/boards/{id}/members`.
- Lists: `POST /api/boards/{id}/lists`, `PATCH/DELETE /api/lists/{id}`
  (PATCH covers rename + reorder via `rank`/`list_id`).
- Cards: `POST /api/lists/{id}/cards`, `PATCH/DELETE /api/cards/{id}`
  (PATCH covers description, rank, move between lists).
- Labels: `GET/POST /api/boards/{id}/labels`, `PATCH/DELETE /api/labels/{id}`,
  `PUT /api/cards/{id}/labels` (set associations).
- Timeline: `GET /api/cards/{id}/timeline`, `POST /api/cards/{id}/comments`.
  (desc/label/attachment events are appended server-side as a side effect of the
  corresponding mutation, with the client supplying `payload_enc`.)
- Attachments: `POST /api/cards/{id}/attachments` (multipart: metadata + cipher
  bytes), `GET /api/attachments/{id}` (cipher bytes), `DELETE`.

Update propagation between collaborators is handled by the sync layer (poll /
refresh-on-focus, and the offline outbox in a later phase) — there is no separate
realtime push channel. See §6.

A later phase maps a **subset of the Trello REST API** onto this (boards/lists/
cards/labels) for migration tooling; deferred.

---

## 5. Frontend

Reuse dope's design system (`styles.css` variables, layout grids, `menu.js`
chrome, `login.html`/`login.js`/`profile.js`) and its no-bundler `window`-globals
convention. New modules:

- `crypto.js` — scrypt (noble) + AES-GCM envelope; `unlockBoard`, `encField`,
  `decField`, key cache (IndexedDB). Sole owner of the wire format.
- `board.js` — board list + kanban view: lists and cards, HTML5 drag-and-drop
  with fractional-rank reordering, optimistic update + SSE reconcile, derived
  card titles (decrypt → first words → fade). Decrypts on render only.
- `card.js` — card detail: monospace plain-text description editor, timeline
  (comments + desc diffs rendered two-up, label/attachment events), label
  picker, attachment upload (webp recompress, lossless checkbox).
- Unlock UI — passphrase prompt when a board's DK isn't cached; create-board
  flow that sets the passphrase.

Frontend tests in node (`node --test jstest/*.test.js`): crypto round-trip, envelope compatibility,
rank-insertion math, title derivation.

---

## 6. Special behaviors

**Test lists / test cards.** A list of `type='test'` holds `kind='test'` cards:
- Title is a date/time; "description" is a list of rating.chgk.info player IDs
  (stored in `description_enc`).
- Creating a test card auto-creates two board labels: green
  `"{yyyy-mm-dd HH:MM} взяли"` (`kind='test_taken'`) and red `"…не взяли"`
  (`kind='test_missed'`), which the user then assigns to questions manually.
- `board_player_map` resolves player IDs → names for display.

**Copy / move across boards.** Client-side re-encryption (§2): decrypt under
source DK, re-encrypt under target DK, recreate structure via the API, reconcile
labels by decrypted name+color (create missing at target). Carries description,
labels, timeline, and attachments.

**Attachments.** Client recompresses images to WebP q70 via canvas/OffscreenCanvas
unless "lossless" is checked, then encrypts and uploads ciphertext. Download
fetches ciphertext and decrypts in the browser.

**Collaborator update propagation.** No realtime push for now. Clients pick up
others' changes via the sync layer — a pull on board open / refresh-on-focus in
M1, and the offline outbox + reconcile loop in a later phase. (Live SSE co-editing
can be revisited later only if poll latency proves annoying.)

---

## 7. Milestone 1 work breakdown (build order)

1. **Scaffold.** New `xy` Go module; copy dope's server skeleton (embed, gzip,
   ETag/cache-busting, dev disk-read mode), `justfile`, `deploy.py` patterns,
   SQLite WAL + migration runner + write-tx discipline.
2. **Auth port.** `users/sessions/invites/telegram_login_codes`, login/register
   handlers, session middleware, `session` package, design-system assets,
   `login`/`menu`/`profile`. Port the Telegram bot (`cmd/telegram-bot` +
   bridge) with its own token.
3. **Crypto foundation.** `crypto.js` (scrypt via @noble/hashes + AES-GCM envelope), board
   create (generate DK, wrap, verify token), unlock UI, IndexedDB key cache,
   strict CSP + SRI. node round-trip tests.
4. **Data model + API.** boards/members/lists/cards/labels/card_labels/timeline/
   attachments tables + endpoints, write-tx, ACL checks.
5. **Board UI.** Board list, kanban view, drag-reorder (fractional ranks),
   derived titles, optimistic updates.
6. **Card UI + timeline.** Monospace description editor, comments, desc diffs,
   label management.
7. **Attachments.** WebP recompress + encrypt + upload/download; blob store.
8. **Test lists/cards.** Special list/card type, auto labels, player map.
9. **Copy/move across boards** with client-side re-encryption + label reconcile.
10. **Tests + deploy.** `just pre-commit` (fmt/vet/test), deploy script.

---

## 8. Later phases (sketched)

- **Offline / PWA.** Service worker for app shell; IndexedDB mirror of board
  ciphertext + a mutation **outbox**; background sync on reconnect. Fractional
  ranks + per-field encryption + soft deletes make last-writer-wins-per-field
  merges tractable; flag genuine conflicts in the timeline. Design the API now so
  every mutation is an idempotent, replayable delta (client-supplied op id).
- **Encrypted search.** Client-side index in IndexedDB built from decrypted
  content as boards are opened; search runs locally. (Server-side encrypted
  search is out of scope.) Possibly a future blind-index option for shared
  boards.
- **Trello API compatibility.** Map a subset of Trello's REST surface onto §4 to
  ease client migration.
- **chgksuite import/export.** Bidirectional conversion of boards/test data.

---

## 9. Open questions / risks to revisit

- **scrypt parameters** vs. mobile UX — needs measurement on a locked-down
  iPhone (pure-JS, no WASM/SIMD, so slower than native); start `N=2^15, r=8, p=1`
  and tune. KDF id + params are stored per board so they can rise over time.
- **Metadata leakage** (§1) — accepted for M1; revisit if a stricter privacy bar
  is required (would mean encrypting associations/positions and losing cheap
  server-side ordering).
- **Single JS dependency** `@noble/hashes` (pure JS, audited, no WASM) — vendored
  self-hosted and pinned with SRI; it's the only third-party client code.
- **Offline conflict semantics** — decide per-field LWW vs. surfacing conflicts;
  affects the M1 API delta shape, so lock it in before §7.4 freezes endpoints.
