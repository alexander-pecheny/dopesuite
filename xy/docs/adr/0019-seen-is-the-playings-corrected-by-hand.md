# Seen is the Playings corrected by hand, and a Declaration names people

Until #90, who saw a question was exactly the testers of the Sessions it had a
Playing with. Two real cases did not fit. When an editor moves questions in from
an old pool, they know who saw each one, but there was no sitting on this board
to record it against, so they had to invent a "virtual" Session per group of
people. And when somebody came late to a sitting and missed questions 1–3, the
only honest record was to split that Session in two.

So a Card now carries two corrections in `cards.seen_enc`, encrypted like the
rest of its content (`seen.ts`):

- **extra**: people who saw the question outside any Session.
- **absent**: per Session, testers who were there and missed this question. The
  Session is named by its `key` (ADR-0003), not its row id, because the key
  survives a Transfer and the Playings are reconciled by it on the other side.
  A Session from before keys existed gets one written the first time an absence
  is recorded against it.

Seen = the Playings' testers, minus the absences, plus the extras. A person is
their trimmed name, the identity the Tester List already deduped by.

## The Tester List had to follow

The custom names whoever saw more than half of a tour. That used to be counted
per Session, which was already wrong for somebody at two sittings that each
played half the tour, and becomes meaningless once a person can see a question
with no Session at all. It is now counted per person, over Seen.

The Declaration (which people the preamble names) had to follow as well: it
could only tick Sessions, and a person added by hand belongs to none. It is now
an encrypted list of names, one row per tour in `tour_declarations`. The old
`tour_testers` rows are not migrated, because the server cannot turn a session
id into names. The client reads a tour that still has only those rows as
everyone who was at the Sessions it named, and the first new Declaration of that
tour deletes them. An old client writing `tour_testers` deletes the tour's
`tour_declarations` row in turn, so whichever wrote last wins.

## Considered options

- **A quick-add that creates an undated Session.** No schema change, and
  Transfer already knows Sessions. But a Session is a sitting, and these are
  not; the Tests panel would fill with one-person rows; and with per-Session
  counting, somebody added to ten questions of twelve would be ten Sessions that
  each saw one.
- **The absence on `card_sessions`.** It would scope itself and cascade by
  itself. But names are content and must be encrypted, which means a second
  encrypted column on a join table for a correction that is rare, and a second
  write path. On the Card it rides the existing card PATCH, the offline mirror,
  Transfer and the Bundle the way `alias_enc` does.

## Consequences

- Transfer and the Bundle carry `seen` with the Card. The Bundle gains an
  optional `tour_declarations`; bundles made before it still import, through the
  old `tour_testers` path.
- A Declaration by names is frozen: a tester added to a Session after the tour
  was declared is not named until somebody ticks them. That is the same as
  ticking a person by hand, and a tour with no Declaration still follows the
  custom live.
- The mass-action bar has «Добавить видевших» and «Не видели», which apply the
  same two corrections to every ticked question.
