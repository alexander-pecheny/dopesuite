# xy — encrypted question boards

Trello-style boards for editing ЧГК questions. All content that users enter is encrypted in the browser, separately for each board, and the server only stores ciphertext plus the structural metadata it needs. Board names are the one deliberate exception and are stored as plaintext.

## Language

**Board**:
One encrypted workspace: lists of cards, all under a single key derived from a passphrase, with its own members and ACL. It is unlocked in the browser, and the server never sees the content in plaintext.

**List**:
One ordered column of Cards on a Board.

**Card**:
One question, one Theme, or a note. It holds encrypted content in 4s form, plus Labels, a Timeline, Attachments and an optional Alias.

**Theme (тема СИ)**:
One Своя игра theme, held as a single Card. It has a name, optionally an author and a comment of its own, and a **Ladder** of questions. Those questions are ordinary questions in every respect except their number, which is their point value. A theme is written, tested and judged as a whole: if the 30 turns out to be easier than the 20, that is a fault of the theme rather than of either question. That is why a theme is one Card and not five (ADR-0018).
_Avoid_: round, category, block

**Ladder**:
The questions of a Theme, in order, each under its own `№`. The order is what determines the points. The first question is worth whatever its `№` says. Moving a question up swaps its CONTENT with its neighbour's, while the `№` values themselves stay where they are. That is what lets a theme numbered 10/30/50 by an import, or one that runs past 50, survive being reordered. After the five that are actually played come the rungs `запас1` and `запас2`. A blank rung is valid 4s and holds its position, which is what lets somebody write the 50 before the 10.
_Avoid_: treating the order of the slots as separate from the points. They are the same thing.

**List Type**:
What a List is meant to hold: either вопросы ОД or темы СИ. It only decides which kind of Card the «Добавить карточку» button creates, and nothing else. Both kinds are still offered on every Card, a List containing a mixture of them is perfectly legal, and changing the type rewrites nothing.

**Alias**:
A Card's own short display label, stored in its own encrypted column. It is deliberately not a 4s marker, because the markers mirror chgksuite byte for byte, and inventing a new one would break parity on import and export.

**List Group**:
A named, ordered run of Lists that are **next to each other**, sharing one question-numbering sequence and exporting together. A group always moves as a single block. In the schema it is called `list_of_lists`.

**Version**:
One candidate form of a Card, kept next to its siblings so that the editors can compare them before choosing. A Version is the whole question — the wording, the ответ, the зачёт, the раздатка and the автор — because a reworded question needs its own зачёт and may come with its own picture. Versions are stored inside the Card's own content, one after another, so they take up no extra column. The first one is what the board displays, and every export merges the rest back into that single numbered question. A Version may have a **Name**, such as «полегче» or «посложнее», which says in one word what «Версия 2» cannot. The Name is for the editors only and never reaches an export. A Version has nothing else: no author and no date. Its rules are in `web/ts/versions.ts` (ADR-0007).
_Avoid_: draft, variant, revision. A Version is a live alternative, not a past state that has been superseded; recording those is the Timeline's job.

**Hidden Comment**:
chgksuite's `(hidden-comment …)`: text an editor writes for other editors, which never appears in any rendering of the question. It is simply a note, unless it is on a line of its own and its payload begins with `xy-version:`. That form both starts a Version and names it, and it is read rather than displayed. Only two views show a note at all: Текст, which is the source verbatim, and Поля, whose fields are raw 4s. Просмотр, the board, what gets copied for a tester, and every export except the .4s all drop it.
_Avoid_: comment. That is the Timeline's word, and nobody can reply to a Hidden Comment.

**Label**:
A named, coloured tag that belongs to a Board and can be put on any of its Cards. It can be renamed and recoloured at any time. An assignment may optionally carry a Playing: without one it is the author's own view of the question, and with one it is what the testers thought at that sitting. That is why «взяли» is a single Board Label combined with a Playing, rather than a separate label for each session.
_Avoid_: tag, or метка as something distinct. Also mark, which is retired: it was the name of the взяли/не взяли slot before an assignment could be scoped to a Playing.

**Label Filter**:
A way of LOOKING at a Board. You pick some Labels and one of все, любая or ни одной, and then every List draws only the Cards that match. It affects the drawing and nothing that is computed from it: every export, «Переместить список» and Transfer all still see the whole Board, so a package can never quietly ship short. A filtered List reads «1, 4, 7», because the number belongs to the question rather than to the view. While the filter is on, a List head says «N из M», and ticking a whole List for a Mass Action ticks what is currently drawn. Both of those report the view rather than redefine it. The filter matches a Label however it was assigned, whether or not it is scoped to a Playing, so a Card can survive on a verdict that its dots do not show; the modal says so explicitly. Nothing is written down and nothing is remembered: the filter dies on reload, like the лента's narrowing and unlike a Feed Default.
_Avoid_: search, which is Find's word and reads content; also view and mode.

**Test Session**:
One sitting at which a group of testers played a set of questions. It is its own entity at Board level, not a Card and not a List, and it carries a date, optionally a time and a timezone, a title, and its testers.
_Avoid_: тест-список, test list, test card

**Playing**:
A record that a question was played at a Test Session. It is a Card's own link to a Session. «Видели» reads it, and a Label can be scoped to it.
_Avoid_: mark, test mark, session tag

**Seen (Видели)**:
Who saw a question. It starts as everyone at every Session the Card has a Playing with, and the Card corrects it by hand in two ways (ADR-0019). A person can be added who saw the question outside any Session, for example in another pool before it moved here. A tester of a Session can be marked absent from this one question, for example because they came late and missed questions 1–3. Both corrections are stored on the Card. The Tester List, the Common Testers and the partial «видели вопросы …» line all read Seen, never the Sessions directly.
_Avoid_: viewers, override, exception

**Author Share (Доля)**:
An author's 1/n part of a question written by several people, added up over the questions of a tour up to a chosen number and shown as a percentage of them. It is what a fee gets divided by. It sits next to the plain count, which is one per question an author is on, and the two only differ where a question has more than one author. Two spellings of a name count as one author when they fold together the way a search folds them, which covers stress marks, spaces, case, and ё against е. Нулевые are left out unless you ask for them: they are played, but they are usually not paid for.
_Avoid_: counting a co-authored question once for each author and calling the result a share.

**Tester List**:
The «Вопросы тестировали: …» line that a tour carries in its preamble. By ЧГК custom it names the people who tested most of the tour and who therefore should not play it. Somebody who saw only one or two questions may still play, skipping the ones they already know. The list is compiled per tour — a List, or a whole List Group — from Seen on its questions, not from the Board's Sessions in general. It is counted per person: somebody at two sittings that each played half the tour saw all of it.

**Declaration**:
Which people a tour's Tester List names. Before schema v26 it named Sessions, and such a Declaration still reads as everyone who was at them until the tour is declared again. This is Board data rather than a per-reader preference, because the preamble belongs to the tour and ships with the package, so two editors preparing it must see the same answer. If a tour has no Declaration, it falls back to the custom: everyone who saw more than half of its questions.
_Avoid_: tick state, selection, pick

**Common Tester (общий тестер списка)**:
A tester the tour's Tester List already names, and who has therefore already been warned off the tour. A Card's «Видели» line leaves them out and names only the extra people, since those are the ones nobody has warned, which is the whole point of the line. «Показать всех тестеров» on the open card brings them back, dimmed. That is a look that lasts as long as the card is open, and never a stored preference.
_Avoid_: main tester, основной тестер, hidden tester

**Announce Set**:
The cities that a Test Session's start time is announced in, and the source of the invitation line an editor pastes into a messenger. Each city carries a timezone and the name to print. Testers are not xy users, so this produces an outbound string rather than a rendering preference.

**Person Directory**:
The tester names this device has seen, collected from every Board whose key it holds. It is a source of suggestions when typing a Test Session's testers. It is never an identity, is never synced, and is not an entity on the server.
_Avoid_: people database, contacts

**Mention**:
An @username in a Card comment that names a member of the Board, resolved against the roster when the comment is posted or edited. The resolved member ids are plaintext event metadata, and they are what the server actually routes notifications by (ADR-0009); the text itself is only the rendering. Replying to a member's comment mentions them implicitly, with no @ needed. A Mention counts for more than an ordinary comment: its unread signal is red where a comment's is blue, and it may reach the member as a nudge on Telegram. It uses the same read watermark as comments, so reading the лента clears both.
_Avoid_: tag, which is a Label's word, and ping, which is the nudge rather than the Mention itself.

**Reaction**:
An emoji — any emoji — that a member puts on a comment or on the Card itself, and removes by putting it on again. Reactions are aggregated into chips on whatever they are attached to, rather than becoming rows in the лента. They count as an ordinary blue unread signal and they appear in the 🔔 feed. Which emoji somebody chose is as private as the text of a comment. Removing one deletes it without leaving a Tombstone, because a Reaction is applause rather than content.
_Avoid_: like, since no single emoji is privileged, and vote, which is what a Label is for.

**Timeline**:
The history of a Card or of a Test Session, in three kinds of entry: comments, which are the discussion; description edits, shown as word-level diffs; and the metadata trail, which records labels attached or removed and attachments added, replaced or deleted. Reactions are stored on the Timeline as well, but they are drawn as chips on their target and never as rows. A comment may belong to a Card, to a Session, or to a question as discussed at one particular session. A reader can narrow the лента to a single kind (see Feed Default). The kinds partition it completely, so there is no entry that is invisible in every mode.

**Feed Default**:
Which kind of Timeline entry a reader sees in the лента when a Card opens. It belongs to the reader and not to the Board: one editor reads for the discussion and another reads for what the question used to say, and they may be reading the same Card side by side. Narrowing the лента inside an open Card is a look at that Card rather than a change to the default, and it ends when the Card is closed.
_Avoid_: feed filter and view mode. The narrowing is temporary; only the default is a preference.

**Interface Font**:
Which of the Kit's six body faces the whole site is set in, for one reader. The choices are Noto Sans, or one of the five that fix what their upstream versions do to Russian: Inter Fix RA, IBM Plex Sans Fix, Literata Fix, IBM Plex Serif Fix and STIX Two Text Fix. It belongs to the reader rather than to the device, so it is stored on the account as `users.ui_font`, the same way a Feed Default is. The browser keeps a copy only because the font has to be on the page before it paints. It has nothing to do with a Board, and no export ever sees it: a PDF is typeset in the fonts the handout pipeline embeds, whatever the reader happens to be reading in.
_Avoid_: theme, which means light and dark and belongs to the Kit; and card font, which is the size of a Card's text on the board and is a Board Size.

**4s**:
chgksuite's plain-text question format, and xy's interchange format for import, for export and for the card editor's Текст view. Parity with chgksuite is byte for byte and is tested against an oracle. Never extend the format on our own.

**Handout**:
Раздатка, in the two senses that the Russian word does not distinguish. First, a Card's own: a field of its 4s, holding either text or the name of one of its image Attachments, and the thing an editor means by «раздаточный материал». Second, a tour's: the `.hndt` source built out of those, rendered to PDF entirely in-process with typst as wasm, which is what «Вёрстка раздаток» produces. The first is content and travels with the question. The second is an artefact, and nothing decrypted ever touches disk on its way there. An image name is not a foreign key: a Card may name a picture that is attached to a different Card, or to no Card at all, and an import does that routinely.

**Board Passphrase**:
The words that wrap a Board's key. They are chosen once when the Board is created, only the owner can replace them, they are stored nowhere, and they cannot be recovered. A device that has unlocked the Board once keeps the key rather than the words, so a Board can stay readable on one phone for years after everybody has forgotten the passphrase.
_Avoid_: password, which is the account's, and master password.

**Passphrase Check**:
A question the Board asks its owner once a month, on a device that holds the key but not the words: either type the Board Passphrase, or set a new one. It is local to the device, it never appears while Тест-режим is on, and it has no «Позже» button, because the only two ways out of it are the two ways the passphrase becomes known again. Members who are not the owner are never asked; they ask the owner.

**Envelope**:
The single wire format for ciphertext: `magic("xy1") | alg | nonce | ct+tag`, base64-encoded inside JSON. `crypto.js` is the only file that owns it. Each board has a random data key (DK) that does the encrypting, and the passphrase-derived KEK only wraps the DK, so changing a passphrase re-wraps it without re-encrypting anything.

**Outbox**:
The queue of mutations made while offline, in `sync.js`. Anything created offline gets a negative temporary id, which is remapped to the real id when the queue is flushed.

**Mirror**:
This device's copy of a Board's ciphertext: the snapshot, and, once it has been prewarmed, the comments as well. It is what lets a Board be opened and searched with no network.

**Search Index**:
What this device is able to find: the readable words of every Board whose key it holds, meaning each Card's 4s, its Alias and the comments on it. It is never synced and is not an entity on the server. It is only ever written by code that already holds a data key, and it is deleted along with that key, so forgetting a Board's password also takes away the ability to find anything in it.
_Avoid_: cache. The Mirror is the cache; this is the readable half.

**Folding**:
The forgiving comparison that a search matches by, so that a stress mark, a non-breaking space or a «ёлочка» left behind by the typography pass cannot hide a question from the editor who wrote it. A replacement deliberately does not fold: finding too much costs you a glance, whereas replacing too much costs you a package.

**Occurrence**:
One matched span inside one Card's 4s. It is the unit an editor ticks before a replacement runs, because «г.» in «г. Москва» and «г.» in «1917 г.» are two separate decisions.

**Transfer**:
Carrying content onto another Board: one Card, a List, a List Group or a whole Board. It can happen live between two Boards this device holds keys for, or through a Bundle. Everything arrives with fresh ids, and nothing on the target is ever overwritten. Only two things are reconciled with something that already means the same thing on the target: a Label, matched on its decrypted name and colour, and a Test Session, matched on its `key` (ADR-0003). A move is a Transfer followed by deleting the source.
_Avoid_: merge and sync, since neither of them reconciles the same entity twice, which is what a sync engine would do; and import, which is a Transfer whose source is a file or Trello.

**Bundle**:
A Transfer in the form of a file: a Board, or any run of its Lists, as one plaintext zip containing `board.json` plus the attachment files. It is created and read entirely in the browser, and it exists for moving content to another xy instance (ADR-0013). It carries everything its Lists' Cards reach: their Labels, the Sessions at which they were played, and the Declarations of those Lists. A List Group travels whole or not at all. Applied to a new Board, a Bundle produces a replica under a fresh key; applied to an existing Board, it appends. Per-user state such as mentions and read watermarks stays behind, as do Tombstones, and members travel with it only as advisory names.
_Avoid_: backup, which is litestream's job, and Board Bundle, since a Bundle need not contain a whole Board.

**Membership**:
A person's place on a Board's roster. It is entirely separate from holding the Board's key (ADR-0017). It is granted by the owner or by an Invite Link, and it is ended by the owner, or by the member themselves, whether or not they know the passphrase: forgetting a Board's password costs you its content but never your way out of it. The owner has no Membership to end, and deletes the Board instead.
_Avoid_: access, permission

**Invite Link**:
A URL that lets whoever holds it join one Board as an editor, in the same way as a Telegram invite link. The owner creates it and pastes it into a chat. It can limit how many times it is used, it can expire, and it can hold the joiner until the owner approves them. It can be revoked, and the owner's list shows how many people came in through it and who they were. It grants MEMBERSHIP and never the key (ADR-0017): the joiner arrives at the passphrase overlay knowing nothing but the Board's name, and the passphrase still has to travel from person to person. This has nothing to do with the instance's registration invite code (the `invites` table, `xy-server invite`), which lets a stranger create an account and knows nothing about Boards.
_Avoid_: invite on its own, since that word is already taken; also share link.

**Join Request**:
Somebody waiting at an Invite Link that requires approval. They have their own row on the link, and they are neither a member nor refused. A Join Request costs the link nothing until the owner approves it, so a link with one seat left may gather a queue and the owner picks from it. A decline is final for that link but never for the Board. The owner finds out about one from the count on «Участники», and, if a bot is configured, from a nudge on Telegram. They never find out from the 🔔, which reads a Card's encrypted events and knows nothing about membership.
_Avoid_: application, pending member

**API Token**:
A credential that lives for a month, can be revoked, and acts as the user (ADR-0015). It is created at /profile/tokens, shown once, and accepted on every API route a session cookie reaches except three: the password, the username and /admin. It authorises but decrypts nothing, so everything it can reach is ciphertext, plus the plaintext Board names. Changing the account password revokes every token at once, which is what to do about one that has leaked.
_Avoid_: API key. There is no key-and-secret pair; the `key` parameter of the Trello-compatible API is ignored. Also password.

**Tombstone**:
Any deleted entity during its 14-day grace period. It is hidden from the app and does not count towards quota, and it can be restored on request. After 14 days it is reaped, which destroys it permanently, including the bytes of any attachment.
