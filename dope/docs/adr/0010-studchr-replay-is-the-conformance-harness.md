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
