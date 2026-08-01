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
One candidate wording of a Card's question, kept alongside its siblings so the editors can weigh them before choosing. Only the question has Versions — never the answer or the comment. They live in the question text itself, separated by chgksuite's `(PAGEBREAK)`, so every Version reaches the export and pruning the rejected ones is an editorial act, not an automatic one. A Version has no name, author or date: it is a wording and nothing else.
_Avoid_: draft, variant, revision (a Version is a live alternative, not a superseded past state — that is what the Timeline records)

**Label**:
A named, coloured tag on a Board, assignable to any of its Cards, renameable and recolourable at will. An assignment carries an optional Playing: unscoped it is the author's own view of the question, scoped it is what the testers thought at that sitting. So «взяли» is ONE Board Label composed onto a Playing, not a label per session.
_Avoid_: tag, метка as a distinct thing; mark (retired — it named the взяли/не взяли slot before an assignment could be scoped)

**Test Session**:
One sitting at which a group of testers played a set of questions. Its own Board-level entity — not a Card and not a List — carrying a date, an optional time and zone, a title, and its testers.
_Avoid_: тест-список, test list, test card

**Playing**:
The record that a question was played at a Test Session. A Card's own link to a Session — it is what «Видели» reads, and what a Label can be scoped to.
_Avoid_: mark, test mark, session tag

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

**Timeline**:
A Card's or a Test Session's history: comments plus recorded description edits (word-level diffs). A comment may belong to a Card, to a Session, or to a question as discussed at a particular session.

**4s**:
chgksuite's plain-text question format — xy's interchange format for import, export, and the card editor's Текст view. Parity with chgksuite is byte-for-byte and oracle-tested; never extend the format unilaterally.

**Handout**:
Раздатка: a `.hndt` source rendered to PDF fully in-process (typst as wasm). Nothing decrypted ever touches disk.

**Envelope**:
The one wire format for ciphertext: `magic("xy1") | alg | nonce | ct+tag`, base64 over JSON. `crypto.js` is its sole owner. Per board, a random data key (DK) does the encrypting; the passphrase-derived KEK only wraps DK, so a passphrase change re-wraps without re-encrypting.

**Outbox**:
The offline mutation queue (`sync.js`): entities created offline get negative temp ids, remapped to real ids on flush.

**Tombstone**:
Any deleted entity during its 14-day grace period: hidden from the app and from quota, restorable on request. After 14 days it is reaped — permanently destroyed, including attachment bytes.
