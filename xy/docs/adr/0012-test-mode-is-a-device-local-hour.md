# Test mode is a device-local hour

## Status

Accepted, 2026-08-20.

## Context

Running a test session used to cost a click per fact: every question shown to
the testers had to be added to the test by hand, and every comment written
during the sitting had to be tagged with it from its ⋯ menu afterwards. Two
editors of one packet sometimes run test sessions simultaneously, so any
server-side notion of "the currently running test" would have to model who is
running what — a mess the feature does not need.

## Decision

Test mode is a fact about a device, not about a board. One localStorage slot
(`xy-testmode`, testmode.ts) holds the active board, session, a last-activity
stamp and a do-not-remark list. The server knows nothing; two editors each run
their own mode on their own device.

The rules, each chosen deliberately:

- **One test per device.** Pressing ▶ on any session replaces the active one
  silently — you only start a test you are about to run.
- **An idle hour ends it, by wall clock.** Any pointerdown or keydown on any
  xy page refreshes the stamp (pwa.ts, throttled to one write per 30s); every
  reader compares timestamps, so the hour runs while the tab is closed or
  asleep. Nobody's Tuesday test resumes on Wednesday morning.
- **A minute on an open card marks it.** The dwell stamp is set on open and
  judged against the wall clock, so a backgrounded tab's throttled timer marks
  late rather than never (visibilitychange and a minute tick are the
  catch-ups). Closing or switching cards resets the stamp; eligibility is
  decided when the minute is up, not when the card opens.
- **A comment is born tagged, and marks its card.** Both comment write paths
  send `session_id` (a field the API already had) and then ensure the card
  carries the test — the ⋯ tag menu only offers the card's own tests, and a
  tag that menu cannot reproduce helps nobody. Replies count; reactions have
  no session_id path at all.
- **A hand-removal wins.** Taking the active test off a card puts the card on
  the mode's do-not-remark list: the automation never fights a human
  correction. The list dies with the mode.
- **Marking is silent.** The chip appearing is the feedback; the mode exists
  to be unattended. The topbar badge (flask + session name, board.ts) is the
  one persistent signal, and clicking it ends the mode.

## Consequences

- The kernel (createTestMode, createDwell) is pure over an injected clock and
  store, tested in jstest/testmode.test.js; board.ts owns the wiring.
- Marks ride the existing whole-set `setCardSessions` verb, so they queue
  offline like any other write.
- A session deleted while live switches its mode off; a session renamed
  renames the badge on the next render, because nothing but the id is stored.
- The server still cannot tell an auto-mark from a hand-mark — by design.
