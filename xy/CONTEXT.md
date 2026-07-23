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

**Timeline**:
A Card's edit history: comments plus recorded description edits (word-level diffs).

**4s**:
chgksuite's plain-text question format — xy's interchange format for import, export, and the card editor's Текст view. Parity with chgksuite is byte-for-byte and oracle-tested; never extend the format unilaterally.

**Handout**:
Раздатка: a `.hndt` source rendered to PDF fully in-process (typst as wasm). Nothing decrypted ever touches disk.

**Envelope**:
The one wire format for ciphertext: `magic("xy1") | alg | nonce | ct+tag`, base64 over JSON. `crypto.js` is its sole owner. Per board, a random data key (DK) does the encrypting; the passphrase-derived KEK only wraps DK, so a passphrase change re-wraps without re-encrypting.

**Outbox**:
The offline mutation queue (`sync.js`): entities created offline get negative temp ids, remapped to real ids on flush.
