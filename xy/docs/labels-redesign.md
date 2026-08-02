# Labels redesign

> **Partly superseded.** The Mark model below — a label bound to a session, one
> «взяли» per sitting — was replaced mid-build by
> [ADR-0004](adr/0004-a-label-assignment-carries-an-optional-playing.md): a label
> is an ordinary board label and its ASSIGNMENT carries an optional Playing.
> Where this note and ADR-0004 disagree, the ADR is what shipped. Sections marked
> **[superseded]** are kept because the reasoning that led to the ADR is in them.
>
> Three later additions are not described here at all, having been asked for
> after it was written: the per-tour Tester List modal, the card's «Видели
> вопрос, кроме общих тестеров списка» line, and the bundled ЧГК town list with
> its GeoNames timezones.

Covers [#25](https://code.pecheny.me/pecheny/dopesuite/issues/25),
[#33](https://code.pecheny.me/pecheny/dopesuite/issues/33),
[#12](https://code.pecheny.me/pecheny/dopesuite/issues/12),
[#8](https://code.pecheny.me/pecheny/dopesuite/issues/8),
[#7](https://code.pecheny.me/pecheny/dopesuite/issues/7),
and — unplanned, but it falls out — [#2](https://code.pecheny.me/pecheny/dopesuite/issues/2).

## The one idea

Today a test label's only tie to its test session is the string baked into its
name at creation (`"2026-07-20 19:00 Иван Иванов и др. взяли"`). Every one of the five
issues is that string: it can't be renamed (#25), it's too long (#8), it carries
a timestamp nobody can re-render in another zone (#7, #33), and it says
взяли/не взяли when the user wanted "видел" (#12).

So: **a test label stops being a label with a name and becomes a link from a
card to a test session, plus a mark.** Its display text is derived on render,
never stored. Everything below follows from that.

## Model **[superseded — see ADR-0004]**

| now | after |
| --- | --- |
| `labels.kind in (normal, test_taken, test_missed)` | `labels.kind in (normal, test)` |
| test label's name baked at creation | `labels.session_id` + `labels.mark`; name derived |
| a label's colour is its own | a test label's `color_enc` is nullable — null takes the board template's colour for that mark |
| test session = a `test` card in a `test` list | `test_sessions(id, board_id, meta_enc)` — a board-level entity, no list, no card |
| `datetime` one string | `meta_enc = {date, time?, tz, cities[], title, testers[], key, origin?}` |
| a comment belongs to a card | `timeline_events.card_id` nullable, `session_id` alongside it |

`mark` is what tells one of a session's labels from another — `taken`, `missed`,
`seen`, or anything a template names. It's an open string, not an enum.

The Russian for it is «отметка», one letter off «метка», the UI's word for a
label. Keep them off the same screen: the label picker shows chips and needs no
field name, and the mark template is edited in its own dialog, not inside the
card's «Метки» section.

`key` inside `meta_enc` is a random id minted once and copied verbatim on
transfer; it is how a session recognises itself on another board. It stays inside
the envelope so the server can't see that two boards share a session. `key` means
"the same sitting", not "the same row" — a transferred session is a dated
snapshot, never a replica, per
[ADR-0003](adr/0003-a-transferred-test-session-is-a-dated-snapshot.md).

Normal labels are untouched: they keep `name_enc` and `color_enc`.

A test label keeps a `name_enc` too, but only as a cache for chgksuite. The
Trello-compatible API hands every label's name to it (`trello_compat.go:182`),
which chgksuite decrypts and — under `--labels` — groups whole lists by
(`board.py:376`, `:504`). So the client writes the canonical `дата · название`
into `name_enc` at creation and rewrites it whenever the session's date or title
changes. It cannot drift, the per-user display preference stays an in-app
concern, and chgksuite needs no release. The *link* is still `session_id`, which
is what actually fixes #25 and #8.

## Test lists go away

A test session is not a question, and a column of them is not a kanban list. It
was put in one because a list was the container that already existed. Making
`test_sessions` a board-level entity lets the whole special case go: the
`list.type === "test"` branches in numbering, export scope, group membership,
card previews, the kind selector and the Просмотр tab all disappear, and #2 —
"нельзя сходу разобраться, что такое тест-список", with its own suggestion to
split the two entities — closes on the way past.

The replacement surface is a «🧪 Тесты» panel in the board ☰ menu: one row per
session showing date, title and tester count, click to edit, plus the
copy-tester-summary action the test list has today. Sessions sort by date, which
is a better order than the manual rank a list gave them.

Sessions are flat per board. Two tournaments' worth of tests on one board are
told apart by their titles, and the question's own tags are what actually
associate a session with a question — so no test-list ↔ question-list link is
needed. If one board ever really does hold two unrelated tournaments, that's an
argument for two boards.

## What each issue becomes

**#25 — edit labels.** New «🏷️ Метки» item in the board ☰ menu: one row per
label, name and colour editable inline, with a usage count and delete. A test
label's name is locked there **[superseded — there are no test labels; every
label is editable]**. Separately, render test labels with their own affordance — a 🧪
dot or an outlined pill — so an imported Trello tag can never *look* like
«взяли» whatever colour it got. That alone fixes the reported symptom.

**#8 — label naming.** Derived name = a per-user setting on `/profile`:
`дата · название` (today's behaviour) / `название` (date as fallback) / `дата`.
Nothing to migrate, since nothing is stored — flipping it re-renders the board.

**#33 + #7 — date, time, timezone.** The new-session form asks for a date; time
is a separate optional field. What's stored is wall-clock + zone, not an
instant — «19:00 по Москве» is the anchor the editor actually means — with `tz`
defaulting to the author's profile timezone.

The output is not a rendering. Nobody who needs the time in another zone is
looking at xy: testers are invited by messenger and show up on a video call, and
99% of them will never have an account. So the session gets an **announce set**
of zones and a copy action producing one line to paste into that messenger:

> 20 июля, 19:00 (Берлин) / 21:00 (Москва) / 23:00 (Алматы)

It sits beside the «Вопросы тестировали: …» copy the test list has today. Three
things it needs:

- **Cities, not zones, and per session.** `Europe/Berlin` is how you compute the
  time; «Берлин» is what you print. A city carries both. Who's invited changes
  from test to test, so the set lives on the session, seeded from a `/profile`
  default. A small table covers the usual ЧГК cities; anything else is a zone off
  `Intl`'s list plus a name you type.
- **Parentheses, so there's no grammar to get wrong.** `19:00 (Москва)` needs no
  declension, which is what lets a typed city name be safe.
- **Date rollover.** 23:00 Алматы can be tomorrow. The date leads the line once,
  and any city whose local date differs carries its own.

Labels then show no time at all — `дата · название` per #8, and shorter for it.
The time belongs to the session row, where the copy button is.

**#12 — who saw the question.** Two halves, and only the first is in scope here.

The marks half **[superseded — ADR-0004 retires marks entirely; «взяли» is one
ordinary board label composed onto a Playing]**: a **per-board** mark template, seeded at board creation from the
creator's personal default on `/profile`, defaulting in turn to today's
взяли/не взяли pair. Per-board, not per-user, because labels are shared with the
board's other members — two people with different personal templates would
otherwise produce a board with inconsistent mark sets. A user who only cares
about presence sets one neutral mark («видели»), and then tagging a question with
the session *is* the record that those people saw it: one click, no colour noise.

The card detail gains a derived «Видели» line: every tester from every session
the card is tagged with, deduped — including sessions that arrived as copies from
other boards, which is the case the whole thing exists for. That line is the whole point of
the refactor — with the session bound instead of stringified, it is a pure
derivation and stores nothing.

The people-directory half (add Иван Иванов once, reuse him in every session) gets
its own section below. Board-scoped autocomplete — the plain `suggestWrap` the
Автор/Источник fields already use, over the names in the board's own sessions —
ships with this step regardless, because it's free and it's most of the value on
a board that already has tests.

**Profile fields + first run.** `users.timezone` (IANA, plaintext beside `sizes`
and `card_title`) and the existing `default_author`, plus `users.onboarded_at`.
Any page with `onboarded_at` unset opens a modal asking for exactly those two,
timezone prefilled from `Intl.DateTimeFormat().resolvedOptions().timeZone`.

`users.timezone` does two jobs and neither is rendering: it's the default anchor
zone for a new session, and the first member of that session's announce set. The
rest of the default announce set is a separate `/profile` setting, edited next to
it — the zones you keep inviting people from. Both stay *out* of the first-run
modal, along with the label template: two questions is what a new user will
answer, and the announce set is meaningless before the first test.

## Comments belong to a session too

A comment can name the session it came out of: «на этом тесте команда споткнулась
о формулировку». `timeline_events` gains a nullable `session_id` beside a
`card_id` that becomes nullable itself, which gives three shapes — a comment on a
question (today's), a note on a session with no question, and a comment on a
question *as discussed at a particular session*.

The payoff is a debrief view: open a session, read everything said about any
question at that test. Today that's unrecoverable, because a comment records only
which card it sits on.

It also decides the migration. A comment on today's test card is already a note
about the session with no question attached, so it has a home to move to and the
archive residue shrinks to attachments only.

Attaching is never automatic. When a card carries exactly one session, the
comment box offers it as a chip; otherwise you pick. `copyCardExtras` remaps the
session on transfer the same way labels do, by `key`.

## The person directory is a client-side derivation

Client-side, accumulated from the boards this device has unlocked. Not a server
entity, not a synced store, not a new key — and a name it suggests is a name the
user has already read.

(A server-side directory would need a per-user key, and registration is
Telegram-only: `password_hash` is NULL on most accounts, so there is nothing to
derive one from without inventing a fourth passphrase.)

**A plaintext cache in localStorage, keyed by board.** Deliberately outside the
ciphertext-only rule, and the carve-out is cheap: the same device caches every
unlocked board's DK in `xy-keys`, so anything that can read the name cache can
already decrypt the questions. Names are not the secret here, and they are not
much of a secret anywhere.

Writing it is a side effect of loading a board — the tester names are plaintext
in hand at that moment, so there is no separate pass and nothing to decrypt.

**Every unlocked board feeds it, boards others shared included.** A co-editor's
board is exactly where you meet testers worth reusing. Each suggestion is tagged
with the board it came from, so a name you don't recognise says where it's been.

**Forgetting a board's password purges its names.** That's the one case where the
cache would outlive the key that justified it: after «Забыть пароль доски» the
board's content is ciphertext with no key on the device, and its names shouldn't
still be sitting in the clear. One `delete` on the board's entry, in the handler
that drops the DK.

**It only ever suggests.** The tester input lists board-local names first, then
names from other boards tagged with the board they came from (board names are
plaintext, so the tag leaks nothing). Typing still produces a plain string in the
session. No identity, no merge, no dedupe across spellings — that would be a data
model, and this is a dropdown.

So it lands inside step 4 rather than earning a step of its own.

## Cross-board transfer

Copying or moving a question already reconciles labels by decrypted name+colour
(`carddetail.ts#reconcileLabels`). That breaks the moment two users render names
differently, so it becomes: for each test label on the card, decrypt its
session's `meta_enc`, look for a session on the target board with the same `key`,
create it there re-encrypted if absent, then — **[superseded]** where this said
"find-or-create the label for `(session, mark)`", what ships is: match the board
label by name+colour and scope the ASSIGNMENT to the copied playing. Since both keys are in hand during the copy, the match is
client-side and the server learns nothing.

Every design has to *copy* the session — boards share no key, so nothing can be
referenced across one. The `key` field is only what stops the second question
from the same test creating a twin. With sessions board-level there is nowhere
to put the copy but the target board's Тесты panel, which is the answer to
"which list does it land in".

The copy then diverges from its original the moment either is edited, and the
tester list is the field most likely to be edited after the fact — which is the
field «Видели» reads. So the copy records `origin: {board_name, copied_at}` and
the Тесты panel shows «копия с доски N от 3 марта»: a stale answer that at least
says how stale. A «обновить из исходной доски» action is possible later, but only
when both boards are unlocked on the same device, so it can never be the
guarantee. [ADR-0003](adr/0003-a-transferred-test-session-is-a-dated-snapshot.md)
has the reasoning.

## Migration

The binding already exists in the data: `addTestCard` assigns the pair it creates
to the test card itself, so `card_labels` for a `test` card already names its
session's labels.

1. One `test_sessions` row per `test` card — every one of them, with no
   branching. `meta_enc` is the card's `description_enc` **verbatim**: the server
   cannot decrypt, so it moves the ciphertext across and the client folds the old
   `{datetime,title,testers}` shape forward on first read
   (`sessions.ts#parseSession`, the same trick that already folds the older
   `{players:[ids]}` shape). `tz` and `key` are filled in on the first edit.
   Comments on the card move onto the session as session-only notes (`card_id`
   NULL), which is what they always were.
2. A test card carrying **attachments** stays put: the bytes have nowhere to go,
   so it becomes an ordinary card and its (now ordinary) list keeps it. It sheds
   the session's own labels either way — they belong to the session, not to the
   card that used to represent it. The
   planned «Тесты (архив)» list turned out to be unbuildable — a list title is
   encrypted, and the migration cannot decrypt, so it cannot name one either. The
   original list's own title is a better label than an invented one anyway. A
   test card with nothing but comments leaves nothing behind, because step 1
   rehomed them; on most boards that is all of them.
3. **[superseded]** Each `test_taken`/`test_missed` label was to become a
   session label with a `mark`. What SHIPPED: the label becomes an ordinary
   board label keeping its exact name, and every question it marked gains a
   Playing instead (ADR-0004).
4. A `test_taken`/`test_missed` label with no test card (Trello import maps
   green/red that way, see `import.ts`) → `kind = normal`, name kept. These are
   exactly the labels #25 complained about, and demoting them makes them
   editable.
5. **[superseded]** There is no mark template to seed.
6. ~~Each `label_add`/`label_remove` timeline payload gains `label_id`~~ — not
   done, and not doable: `payload_enc` is encrypted and the migration holds no
   key. New writes carry `label_id`; pre-migration history keeps only the frozen
   name, which the renderer falls back to. So a rename updates history from here
   on, not backwards.

`board.ts#deleteCard`'s hand-rolled sweep of a deleted session's exclusive labels
becomes an FK cascade.

## Renaming a label rewrites its history

`board.ts:2587` freezes the label's name into every `label_add`/`label_remove`
payload. That was harmless while labels were immutable; #25 makes it a bug, and
not only for test labels — rename any label and its whole history keeps showing
the old name.

The payload becomes `{label_id, label}`: render live from `label_id`, fall back
to the frozen string when the label has been deleted. That keeps the property the
frozen name was there to protect — a deleted label's history stays readable —
while a rename or a session edit updates every entry that still has a referent.

## Offline

Sessions need no work to survive offline: `sync.ts#substituteValue` rewrites any
negative integer that has a mapping, with no per-field schema, so a label body
carrying `session_id: -7` remaps itself on flush. Cross-board transfer stays
online-only, as it already is.

## Order to build it

1. Label editor + the test-label affordance — closes #25 alone, touches nothing
   else.
2. Profile timezone + first-run modal — independent, and #7 needs it.
3. `test_sessions` + the Тесты panel + derived names — the refactor, and the big
   one. Closes #8, #33, #7, #2.
4. Mark template + the «Видели» line + the person directory — closes #12.
