# A board is downloaded as the changes since the last read

On 2026-11-04 a reviewer on a slow connection waited 3 to 9 seconds each time
board 73 loaded. The server answered in 20 ms; the time went on the transfer.
Each load downloaded the whole snapshot (about 100 KB) and every comment on the
board for the Search Index (about 240 KB), and a board loads whenever its tab
comes back to the front and whenever the outbox drains. About 85% of a snapshot
is card descriptions, and they are ciphertext, which does not compress.

**Every change to a board's cards and comments gets a number.** `boards.rev`
counts them. A trigger increments it on each change and stamps the changed row
with the new value:

- `cards.content_rev` and `cards.rev` when what a card says changes (its text,
  alias, seen, handout settings or kind) and when it is created;
- `cards.rev` alone when it only moves or is deleted;
- `cards.rev` when its labels, Playings or votes change;
- `timeline_events.rev` when a comment on a card is added, edited or deleted.

SQLite has one writer at a time, so the numbers follow the order of commits,
and a reader that has seen rev N has seen every change up to N. Triggers rather
than code in the handlers, because there are 75 write transactions and a
handler that forgot the bump would leave clients missing a change until they
next read the whole board.

**A client sends the rev it holds and gets what changed after it.**
`GET /api/boards/{id}?since=N` returns the snapshot's small parts in full
(lists, groups, labels, sessions, declarations, unread flags, prefs), and of
the cards only those stamped after N: whole when the content changed,
`{id, list_id, rank, same: true}` when it only moved or its sets changed, and
an id in `gone_cards` when deleted. The labels, Playings and votes come for the
changed cards only. `GET /api/boards/{id}/comments?since=N` does the same for
the Search Index. Without `since` both answer as they did before, for clients
running older code and for xy-cli.

**The delta is merged onto the base, not the mirror.** The mirror also holds
edits made offline, and an edit the server rejected would survive every merge.
So the device keeps the last snapshot exactly as the server sent it beside the
mirror (`store.ts`, key `base:<board>`), merges each delta onto that
(`snapdelta.ts`), and writes the result to both.

## Consequences

- **A deletion is only known while its tombstone exists.** The reaper removes
  tombstones after 14 days and records the highest rev it removed in
  `boards.reaped_rev`. A `since` below it, a `since` above `boards.rev` (a
  database restored from a backup) or a `since` that will not parse gets the
  full snapshot. So does a delta that does not fit the base the client holds.
- **A migration that rebuilds one of these tables drops its triggers.** SQLite
  drops a table's triggers with the table, and xy's CHECK changes rebuild
  `cards`. Such a migration must create the triggers again (`revTriggers` in
  `db.go`); `TestRevTriggersExist` fails until it does.
- **`TestBoardDeltaMatchesFullSnapshot` is the guard.** It makes random writes
  of every kind and, after each, checks that a delta from a random earlier
  point, merged as the client merges it, equals the full snapshot. A write path
  the triggers do not cover fails it.
- **The small parts are still sent whole.** They grow with the number of lists
  and sessions, not cards. Giving them revs too would mean tombstones for six
  more tables, for a few kilobytes.
- **The big GETs also carry an ETag.** The snapshot and the timelines are sent
  with `Cache-Control: private, no-cache`, so the browser asks with
  `If-None-Match` and an unchanged answer is a bodiless 304. The response is
  still built on every request, so it never lags a write.
