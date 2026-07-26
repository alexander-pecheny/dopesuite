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

**Label**:
A named, coloured tag on a Board, assignable to any of its Cards. Renameable and recolourable at will — except a Test Label, whose name comes from the Test Session it is bound to.
_Avoid_: tag, метка as a distinct thing

**Test Session**:
One sitting at which a group of testers played a set of questions. Its own Board-level entity — not a Card and not a List — carrying a date, an optional time and zone, a title, and its testers.
_Avoid_: тест-список, test list, test card

**Mark**:
What one of a Test Session's Labels records about a question at that session: взяли, не взяли, видели, or whatever a Board's template names. A Board seeds every new session's marks from its own template.
_Avoid_: role, outcome, verdict, status

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
