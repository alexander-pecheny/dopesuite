---
status: accepted
date: 2026-08-09
---

# СтудЧР-2026 is replayed as the conformance harness

A whole real championship — брейн, ЭК, СИ, ТПШ, ОД — lives in `testdata/` as committed JSON fixtures, and a Go replayer plays it бой by бой through the real handlers, asserting after each Round that standings and the next Round's seating match the sheets the tournament was actually run on. Five formats that share no scheme between them is the strongest available evidence that Structure × Protocol is a model and not five special cases, and unlike a unit test it fails at the Round that broke rather than at a screenshot taken afterwards.

## Considered options

The transcription from `.xlsx` stays in Python (`scripts/studchr/read-*.py`) and runs once, by hand; the replayer is Go so it runs in `go test` beside the model tests it is checking. A Python replayer driving a live server was the incumbent — it is what first populated dopetest — but it needs a booted server and a session token, never runs in CI, and keeps the sheet oracle apart from every other test of the same code.

The same replayer writes a database rather than only assertions, so the demo Fest on dopetest is regenerated from the fixtures CI checks, not hand-built and then feared.

## Consequences

- A fixture joins a бой to a Match by **coordinate** — block, round, index — never by who sits at the table. Matching by participant set only works when seating is already correct, so it can never catch a seeding bug.
- Every seating is tagged given (a Draw, written into Edges before play) or derived (asserted, never written). See CONTEXT.md.
- Where dope and the sheets disagree, the fixture carries an explicit override with a written reason, and `docs/studchr2026-discrepancies.md` is generated from those entries. Any divergence without an override halts the replay. Writing an override is the tournament author's call, never the implementer's — the sheets are evidence of what happened, and overruling them silently is how a demo starts lying.
- A structural override converts a derived seating into a given one: organisers do swap tables on the day, and that is a Draw nobody recorded, not a defect.

## Amended 17 Aug 2026 (item #8 of the architecture review, `2a9cbe8`, `9e21855`)

- The fixtures are transcripts (`docs/replay-transcript.md`), one format for
  every tournament: `[roster]`, `[составы]`, бои by coordinate, `[статистика]`
  and `[таблица s1/g3]` (a Block's or Group's standings, asserted against
  `stage_standings` both ways). Each is emitted by a reader/emitter pair in
  `scripts/studchr/`; the emitters are thin, and every emitter shares
  `transcript.py`.
- One driver, two transports. `serverGame` (`server/tests/replaydriver_test.go`)
  does every Structure read and write itself and hands three things to a
  transport: a match patch, a finish, «рассчитать» on the reseeds.
  `httpTransport` is the old path through the handlers; `directTransport`
  calls the engine the batcher runs per window (`editbatch.PatchMatchTx`,
  `FinishMatchTx`, `RecomputeMatchTx`, the resolver's `ResolveGameSlots*Tx`)
  in its own transactions. The four studchr replays take 25 s direct and run
  on every `just test`; the HTTP twins run under `just test-full`, one per
  game type; a contract test plays the mini transcript through both.
- One codec per Protocol (`replay.Codec`: individual or team, seat form, the
  Σ metric, the three stat columns, the aggregate) — no game name in
  `parse.go`, `run.go` or the driver. `docs/studchr2026-discrepancies.md` is
  generated from the overrides by a test that fails when it is stale.
- The tables earned their keep at once: a boundary reseed re-ranked place-1
  finishers only, the flat Ranker shared ranks on бой place, and ЭК's пересев
  rule was not what the sheet did — all found by `[таблица]` and fixed.
- Not on the interface: a `Locate(coord)` returning a match id would be the
  storage leaking through, and `Run` has nothing to ask it — the coordinate
  lookup is the adapter's business.

## Amended 24 Aug 2026 (Троечка VIII Octobearfest)

The harness took a second tournament, which is what it was built to be able to
do — a new reader (`scripts/troika/`) and a `Codec`, nothing else. What the
second tournament asked for that the first had not:

- **A Draw inside a группа** seats the participants named, rather than the Edge
  «место N в бою X» a bracket Draw compiles to. Everyone in a round-robin
  already has finished бои, so the Edge form pointed the slot at a result
  instead of at the team the sheet named. Троечка needs it because dope's
  round-robin rotates one way and the tournament's rotated the other: the same
  fifteen pairs, grouped into круги differently.
- **A coordinate counts бои stage by stage**, in the order the block emitted
  them. Троечка plays its бронза and its финал as three бои each at the block's
  one round; by match position alone the two series interleaved.
- **A Protocol whose sheet is coarser than its document.** Троечка's протоколы
  count how many of the three answered a вопрос and never which — so the кресла
  are synthesized, and the Статистика tab, the one thing that reads them, has
  no oracle. A sheet is evidence of what it recorded and of nothing else.

## Amended 5 Oct 2026 (the HTTP twins play the first бой of each kind)

The HTTP twins replayed their whole transcript a second time. On this box
that was 45 s for СИ and 34 s for брейн, most of `go test ./...`, and it
proved nothing the direct replay had not: the twins are there for the
handlers, authorisation and the write path, and those do the same thing on
the fortieth бой of a kind as on the first.

- A twin now sends over HTTP only the first бой of each **kind of input**
  (`replay.Bout.Kinds`): how the seating arrives (a Draw written in, derived
  and checked, a lot drawn through the draw endpoint), which kinds of state
  patch its seats carry (marks, theme players, брейн's buzzer and each
  question count, so a перестрелка's extra rows are one, Троечка's counts,
  a перестрелка, a ставка, a pinned place) and the finish. The бои between
  are played direct, because every бой is seated from the ones before it.
  It stops after the last first-of-a-kind (`Script.CoveringPrefix`), so the
  stats and the tables are left to the direct replay, which stays whole and
  is still the conformance gate.
- The reseed is the one endpoint no kind names: it ranks only once the round
  before it has closed. The twin presses «рассчитать» over HTTP the first
  time a reseed is ready, and plays on until one has been.
- Each twin asserts that the бои it sent over HTTP carried every kind the
  transcript has, and that every endpoint the full replay calls (the state
  patch, the finish, the draw where there is one, the reseed where the scheme
  has one) answered 200 at least once. A new kind of input in a transcript
  gets its own бой over HTTP without anybody editing a test;
  `TestKindsReadsEveryInputField` fails if a new field of a Bout or a Seat is
  neither a kind nor declared not to be input.
- What each twin plays now: ЭК 18 of 25 бои, one over HTTP (every бой is a
  Draw with the same kinds; the rest is played to reach the first ready
  reseed); СИ 76 of 96, two over HTTP; брейн 127 of 132, three over HTTP
  (its longest перестрелка is in a semifinal, s5/r1/w1/m2). Хамса's twin is unchanged:
  its seventh and last бой is the first to carry a kind, so the opening
  that covers it is the whole transcript. Measured alone: СИ 45 s to 9 s, брейн 34 s to 13 s,
  ЭК 3 s to 1 s.
