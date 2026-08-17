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
