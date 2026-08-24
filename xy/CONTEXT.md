# xy — encrypted question boards

Trello-style boards for ЧГК question editing. All user content is encrypted client-side per board; the server stores ciphertext plus structural metadata. Board names are the one deliberate plaintext exception.

## Language

**Board**:
One encrypted workspace: lists of cards under a single passphrase-derived key, with members and ACL. Unlocked client-side; the server never sees content plaintext.

**List**:
One ordered column of Cards on a Board.

**Card**:
One question (or note): encrypted content in 4s form, plus Labels, Timeline, Attachments, and an optional Alias.

**Alias**:
A Card's own short display label, stored as its own encrypted column — deliberately NOT a 4s marker, because markers mirror chgksuite byte-for-byte and an invented one would break import/export parity.

**List Group**:
A named, ordered run of **consecutive** Lists sharing one question-numbering sequence and a combined export. A group always moves as one block. (Schema name: `list_of_lists`.)

**Version**:
One candidate form of a Card, kept alongside its siblings so the editors can weigh them before choosing. It is the whole question — wording, ответ, зачёт, раздатка, автор — because a rewording answers to its own зачёт and may carry its own picture. Versions live in the Card's own content, one after another, so they cost no column; the first is the one the board shows and every export merges the rest back into that single numbered question. A Version may carry a **Name** — «полегче», «посложнее» — so it says in one word what «Версия 2» cannot; the Name is the editors' alone and reaches no export. It has nothing else: no author and no date. Its rules live in `web/ts/versions.ts` (ADR-0007).
_Avoid_: draft, variant, revision (a Version is a live alternative, not a superseded past state — that is what the Timeline records)

**Hidden Comment**:
chgksuite's `(hidden-comment …)`: text an editor writes for editors, which reaches no rendering of the question. It is a note, unless it is a line of its own whose payload opens with `xy-version:` — that one both starts a Version and names it, and is read rather than shown. Only the Текст view (the source verbatim) and Поля (whose fields are raw 4s) show a note at all; Просмотр, the board, what is copied for a tester and every export but the .4s drop it.
_Avoid_: comment (that is the Timeline's word — a Hidden Comment is nobody's to reply to)

**Label**:
A named, coloured tag on a Board, assignable to any of its Cards, renameable and recolourable at will. An assignment carries an optional Playing: unscoped it is the author's own view of the question, scoped it is what the testers thought at that sitting. So «взяли» is ONE Board Label composed onto a Playing, not a label per session.
_Avoid_: tag, метка as a distinct thing; mark (retired — it named the взяли/не взяли slot before an assignment could be scoped)

**Test Session**:
One sitting at which a group of testers played a set of questions. Its own Board-level entity — not a Card and not a List — carrying a date, an optional time and zone, a title, and its testers.
_Avoid_: тест-список, test list, test card

**Playing**:
The record that a question was played at a Test Session. A Card's own link to a Session — it is what «Видели» reads, and what a Label can be scoped to.
_Avoid_: mark, test mark, session tag

**Author Share (Доля)**:
An author's 1/n split of a co-authored question, summed over the questions of a tour up to a chosen number and shown as a percentage of them — what a fee is divided by. Sits beside the plain count (one per question an author is on); the two differ only where a question has more than one author. Two spellings are one author when they fold like a search does (stress marks, spaces, case, ё/е). Нулевые are left out unless asked for: they are played, but usually not paid.
_Avoid_: counting a co-authored question once per author and calling that a share

**Tester List**:
The «Вопросы тестировали: …» line a tour carries in its preamble. By ЧГК custom it names those who tested most of the tour and therefore should not play it — someone who saw one or two questions still may, skipping the ones they know. Compiled per tour (a List, or its whole List Group) from the Playings its questions carry, not from the board's Sessions as a whole.

**Declaration**:
Which Sessions a tour's Tester List names. Board data, not a per-reader preference: the preamble belongs to the tour and ships with the package, so two editors preparing it see one answer. Undeclared, a tour falls back to the custom — everyone who saw more than half its questions.
_Avoid_: tick state, selection, pick

**Announce Set**:
The cities a Test Session's start time is announced in, and the source of the invite line an editor pastes into a messenger. A city carries a timezone and the name to print; testers are not xy users, so this is an outbound string, not a rendering preference.

**Person Directory**:
The tester names this device has seen, gathered from every Board whose key it holds. A suggestion source for typing a Test Session's testers — never an identity, never synced, never a server entity.
_Avoid_: people database, contacts

**Mention**:
An @username in a Card comment naming a Board member, resolved against the roster when the comment is posted or edited. The resolved member ids are plaintext event metadata — the routing truth the server notifies by (ADR-0009); the text is only the rendering. A reply to a member's comment mentions them implicitly — no @ needed. A Mention outranks an ordinary comment: its unread signal is red where a comment's is blue, and it may reach the member as a telegram nudge. It rides the comment read-watermark — reading the лента clears both.
_Avoid_: tag (a Label's word), ping (that is the nudge, not the Mention itself)

**Reaction**:
An emoji — any emoji — a member puts on a comment or on the Card itself, toggled off by putting it again. Reactions aggregate into chips on their target rather than becoming rows of the лента, count as an ordinary (blue) unread signal, and appear in the 🔔 feed. Which emoji stays as private as a comment's text; toggling one off removes it without a Tombstone — a Reaction is applause, not content.
_Avoid_: like (one emoji is not privileged), vote (that is a Label's job)

**Timeline**:
A Card's or a Test Session's history, in three kinds of entry: comments (the discussion), description edits (word-level diffs) and the metadata trail — labels attached or removed, attachments added, replaced or deleted. Reactions ride the Timeline too, but render as chips on their target, never as rows. A comment may belong to a Card, to a Session, or to a question as discussed at a particular session. A reader may narrow the лента to one kind (see Feed Default); the kinds partition it, so no entry is invisible in every mode.

**Feed Default**:
Which kind of Timeline entry a reader's лента shows when a Card opens. Theirs, not the Board's — one editor reads for the discussion, another for what the question used to say, and they read the same Card side by side. Narrowing the лента inside an open Card is a look at that Card, not a change of the default: it dies when the Card closes.
_Avoid_: feed filter, view mode (the narrowing is transient; only the default is a preference)

**4s**:
chgksuite's plain-text question format — xy's interchange format for import, export, and the card editor's Текст view. Parity with chgksuite is byte-for-byte and oracle-tested; never extend the format unilaterally.

**Handout**:
Раздатка: a `.hndt` source rendered to PDF fully in-process (typst as wasm). Nothing decrypted ever touches disk.

**Envelope**:
The one wire format for ciphertext: `magic("xy1") | alg | nonce | ct+tag`, base64 over JSON. `crypto.js` is its sole owner. Per board, a random data key (DK) does the encrypting; the passphrase-derived KEK only wraps DK, so a passphrase change re-wraps without re-encrypting.

**Outbox**:
The offline mutation queue (`sync.js`): entities created offline get negative temp ids, remapped to real ids on flush.

**Mirror**:
This device's ciphertext copy of a Board — the snapshot, and once prewarmed the comments too. What lets a Board open, and be searched, with no network.

**Search Index**:
What this device can find: the readable words of every Board whose key it holds — each Card's 4s, its Alias, and the comments on it. Never synced and never a server entity; it is written only by code that already holds a data key, and dies with that key, so forgetting a Board's password takes its findability with it.
_Avoid_: cache (the Mirror is the cache; this is the readable half)

**Folding**:
The forgiving comparison a search matches by, so that a stress mark, a non-breaking space or a «ёлочка» the typography pass put there cannot hide a question from the editor who wrote it. A replacement deliberately does not fold: over-finding costs a glance, over-replacing costs a package.

**Occurrence**:
One matched span in one Card's 4s — the unit an editor ticks before a replacement runs, because «г.» in «г. Москва» and «г.» in «1917 г.» are two different decisions.

**Transfer**:
Carrying content onto another Board — one Card, a List, a List Group or a whole
Board; live between two Boards this device has keys for, or through a Bundle.
Everything arrives with fresh ids; nothing on the target is ever overwritten.
Only Labels and Test Sessions reconcile, onto an equivalent that already means
the same thing: a Label on decrypted name+colour, a Session on its `key`
(ADR-0003). A move is a Transfer plus a delete of the source.
_Avoid_: merge, sync (neither reconciles the same entity twice — that would be a
sync engine), import (that is a Transfer whose source is a file or Trello)

**Bundle**:
A Transfer in a file: a Board, or any run of its Lists, as one plaintext zip —
`board.json` plus the attachment files — made and read entirely in the browser,
for moving content to another xy instance (ADR-0013). What it carries is what
its Lists' Cards reach: their Labels, the Sessions they were played at, the
Declarations of those Lists. A List Group travels whole or not at all.
Applied to a new Board it is a replica under a fresh key; applied to an existing
Board it appends. Per-user state (mentions, read watermarks) and Tombstones stay
behind, and members ride along as advisory names only.
_Avoid_: backup (that is litestream's job), Board Bundle (it need not hold one)

**API Token**:
A month-lived, revocable credential that acts as the user (ADR-0015): minted at
/profile/tokens, shown once, and accepted on every API route a session cookie
reaches but three — the password, the username and /admin. It authorizes; it
decrypts nothing, so what it can reach is ciphertext plus the plaintext Board
names. Changing the account password revokes every one of them at once, which
is the answer to a leaked one.
_Avoid_: API key (there is no key/secret pair — the `key` param of the
Trello-compatible API is ignored), password

**Tombstone**:
Any deleted entity during its 14-day grace period: hidden from the app and from quota, restorable on request. After 14 days it is reaped — permanently destroyed, including attachment bytes.
