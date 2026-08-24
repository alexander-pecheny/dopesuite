# A Board Bundle is a plaintext zip

## Status

Accepted, 2026-08-24.

## Context

xy is self-hostable, so a user must be able to move a board to another
instance. Every content column is E2E-encrypted under a per-board key the
server never holds, and every id is a SQLite autoincrement integer that means
nothing outside its own database. Any transfer format therefore has to choose:
carry ciphertext plus keymeta (server-streamable, but forever tied to xy's
envelope format and to the source passphrase), or carry plaintext produced in
the client (readable, importable under a fresh key, but an unencrypted file on
someone's disk).

## Decision

The whole-board export is a **Board Bundle**: a zip holding one plaintext
`board.json` (format id `xy.board.v1`) plus the attachment bytes as ordinary
files under `attachments/`. Both halves of the feature run in the browser,
like the Trello import: export decrypts under the board key it already holds;
import creates a **new** board with a fresh key and passphrase and re-encrypts
every field client-side. The server never sees plaintext and gains only three
ciphertext-level endpoints (whole-board timeline read, whole-board attachment
list, board-level timeline import).

The consequences chosen deliberately:

- **Plaintext is the point, and the stated cost.** The bundle doubles as a
  hostage-free backup readable by future tools; anyone who can export already
  has read access to everything through the UI. The UI says the file is
  unencrypted.
- **Import never merges.** It always creates a new board with fresh ids —
  merging two divergent boards is a sync engine, which this is not. Re-import
  and delete the stale copy instead.
- **Zip, not one big JSON.** Attachments are binary and up to 50 MiB each;
  inlining them as base64 costs a third more space and unbounded memory. In a
  zip they travel verbatim, one file at a time, and the artifact is inspectable
  with `unzip`.
- **Everything content-ful transfers; per-user state does not.** The full
  timeline travels, description-edit history included. Members and event
  authors travel as advisory usernames (authors re-matched by username on the
  target, else null); mentions and read watermarks are dropped as
  instance-local; tombstoned rows are not exported.
- **The format is versioned and unstable.** `xy.board.v1` promises only that
  an xy at least this new can import it — not that other tools can rely on it.
- **Failure semantics**: import pre-checks the importer's quota against the
  bundle's whole content before creating anything, and any mid-flight failure
  deletes the half-created board.

## Known limitations

- Deep links in card text (`/board/{id}?card=…`) point at the source
  instance's ids and are not rewritten.
- Card and session creation stamps travel in the file for the record, but
  import re-stamps them: the create endpoints say "now", and the timeline's
  own timestamps — the ones with history in them — are preserved exactly.
- A reaction whose comment was deleted on the source is dropped on import
  rather than reparented onto the card, where it would change meaning.

## Alternatives considered

Ciphertext-verbatim export (dump `_enc` blobs + keymeta) would stream from the
server and keep plaintext nowhere, but ties the file to the source passphrase
and the envelope format, is undebuggable, and needs a whole parallel
server-side import path instead of reusing the proven client-side one.
