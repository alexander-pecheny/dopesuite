# A Bundle is a plaintext zip

## Status

Accepted, 2026-08-24. Widened the same day: a Bundle need not hold a whole
board, and may be applied to one that already exists. See also
[ADR-0014](0014-every-transfer-goes-through-a-bundle.md).

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

The export is a **Bundle**: a zip holding one plaintext
`board.json` (format id `xy.board.v1`) plus the attachment bytes as ordinary
files under `attachments/`. Both halves of the feature run in the browser,
like the Trello import: export decrypts under the board key it already holds;
import re-encrypts every field client-side under the key of wherever it lands.

**What it holds is a selection.** The export picker ticks Lists — a List Group
as one indivisible row, since its Lists share a numbering sequence — and the
file carries what those Lists' Cards reach: their Labels, the Sessions they
were played at, the Declarations of those tours, their Timeline and their
attachments. A whole board is simply every tick. Nothing else leaves: an
unassigned Label and a sitting no exported question was played at stay home,
tester names and all, because handing someone three tours should not hand them
the rest of the board's testing.

**Where it lands is a choice too.** Onto a **new** board — a replica under a
fresh key and passphrase, ranks and rows verbatim — or **appended** to a board
that already exists, which is why that half lives on the board page rather than
on /import: only that page holds the target's key.

The server never sees plaintext and gains only three ciphertext-level endpoints
(whole-board timeline read, whole-board attachment list, board-level timeline
import).

The consequences chosen deliberately:

- **Plaintext is the point, and the stated cost.** The bundle doubles as a
  hostage-free backup readable by future tools; anyone who can export already
  has read access to everything through the UI. The UI says the file is
  unencrypted.
- **A Bundle appends; it never reconciles the same entity.** Everything
  arrives with fresh ids and nothing on the target is read back and
  overwritten. The two exceptions are not exceptions to that rule: a Label
  matching on name+colour and a Session matching on its `key` (ADR-0003) are
  reused because they already mean the same thing, exactly as a live
  cross-board copy has always reused them. Reconciling two divergent copies of
  one entity would be a sync engine, and that is still not this. Importing the
  same file twice gives two copies of its Lists; the picker warns on a title
  already present and leaves the decision alone.
- **Zip, not one big JSON.** Attachments are binary and up to 50 MiB each;
  inlining them as base64 costs a third more space and unbounded memory. In a
  zip they travel verbatim, one file at a time, and the artifact is inspectable
  with `unzip`.
- **A Session that arrives on an existing board is a dated snapshot** — it
  carries `origin: {board, at}` from the file's board name and export date
  (ADR-0003), and the Тесты panel says whose sitting it was. A Session on a
  board created *for* the Bundle takes its meta verbatim: that board is the
  original moved, not a copy of it.
- **Everything content-ful transfers; per-user state does not.** The full
  timeline travels, description-edit history included. Members and event
  authors travel as advisory usernames (authors re-matched by username on the
  target, else null); mentions and read watermarks are dropped as
  instance-local; tombstoned rows are not exported.
- **The format is versioned and unstable.** `xy.board.v1` promises only that
  an xy at least this new can import it — not that other tools can rely on it.
- **Failure semantics**: the quota is pre-checked against the whole selection
  before anything is created. After that the **List is the unit**. Onto a new
  board a failure still deletes the board — it was seconds old and nobody
  else's. Appending, the unit in flight is rolled back and the units before it
  stay, because a board full of real work is not something to unwind, and the
  picker lets the reader re-tick what did not land. A Label or Session created
  along the way is left: a finished unit may already be using it.

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
