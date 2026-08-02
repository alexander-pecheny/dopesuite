# A label assignment carries an optional Playing

A question needs two different opinions recorded about it: the author's («сложный
вопрос») and the testers' at a particular sitting («на этом тесте не взяли»).
The first design gave a Test Session its own labels, one per Mark, seeded from a
per-board template — so «взяли» existed once per session and a board with thirty
tests carried thirty of them, each needing its name derived from the session to
stay readable.

We decided instead to compose: a Label is an ordinary board Label, and its
ASSIGNMENT to a card carries an optional `session_id` (a Playing). Unscoped it is
the author's view; scoped it is what the testers thought at that sitting. «взяли»
becomes one board label used everywhere, and the same label can sit on one card
twice — once as yours, once as a test's.

Mark, the per-session label slot, is retired along with the board's mark template
and its editor. Nothing derives a label's name any more; labels are just labels.

## Consequences

- `card_labels` gains a nullable `session_id` FK and widens its key to include
  it. SQLite compares NULLs as distinct, so the unscoped assignment additionally
  needs `create unique index … on card_labels(card_id, label_id) where session_id
  is null` — without it a double-click or an offline replay silently inserts a
  duplicate row rather than failing.
- Being played at a test is its own fact, not a side effect of labelling:
  `card_sessions(card_id, session_id)` is what the «Видели» line reads. Removing
  one cascades the labels scoped to it, because a label scoped to a playing that
  no longer exists cannot be read — «взяли», but at what? The UI confirms with
  the count first.
- chgksuite improves for free. Scoped assignments appear flat in the Trello
  payload, so `--labels` collects every question taken at ANY test into one
  «взяли» bucket, where before it produced one bucket per session.
- The migration is 1↔1 and needs no decryption: an existing test label becomes a
  plain board label keeping the exact name it already has («26 июля · Иван Иванов и
  др. · взяли»), and each also yields its Playing. Those names are redundant
  under the new model — the playing already says which test — but they are the
  user's own text, so they are preserved rather than regenerated, and can be
  deleted by hand once the board has been re-composed.
- Compiling a tour's Tester List stops being free. The test list used to BE that
  list, one per tour; a board-level Session panel can only say who tested at all.
  So a tour's «Вопросы тестировали» line gets its own surface, counting each
  session's Playings across the tour's questions — a session on 12 of 12 and one
  on 2 of 12 carry different obligations under the custom, and the flat list
  cannot tell them apart.
- migrateV18 was rewritten in place rather than followed by a v19: it had run
  only on staging, so there is no deployed schema to upgrade FROM.
