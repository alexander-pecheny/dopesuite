---
status: accepted
date: 2026-07-23
---

# Protocol state is one JSON blob per match, scored in Go

Per-match Protocol state is persisted as a single JSON document on the match row, whose schema the Protocol owns; a Go scorer materializes places and metrics into `match_results`, which is all the Structure layer ever reads. EK's relational `themes`/`answers` tables retire. We rejected per-protocol relational tables (every new format costs tables, migrations, and load/save code — the failure mode of the first brain attempt) and a universal generic-rows table (stringly semantics, contorts non-grid protocols like media rounds).

## Consequences

- Adding a format touches no schema: a state type, a scorer, a renderer.
- The journal, SSE deltas, and checkpoints are already JSON-shaped; the replay engine loses its relational-reconstruction path.
- Cross-match queries into protocol internals (player stats) scan blobs — acceptable at fest scale (whole prod DB is ~50 MB), and materialized `match_results.metrics_json` covers the common aggregations.
- A match blob is KBs; edit latency and SSE fan-out must not regress vs the current path (loadtest suite is the gate).
