---
status: accepted
date: 2026-07-23
---

# Big-bang migration with a parity gate; journal history is rewritten

Prod data (all fests, EK relational state, OD/KSI blobs, the journal) converts to the unified model in one pass during a quiet window between fests, gated on parity: results, exports, and views rendered from the converted DB must byte-match the old code's output for every historical fest, and replaying the rewritten journal from genesis must reproduce the converted final state. The journal's historical records are rewritten into the new opcode vocabulary (e.g. `OpMark` becomes a pointer-set on the converted match blob) rather than kept behind a legacy decoder forever or frozen as a non-replayable archive — one vocabulary, full undo/history depth preserved. Feasible because the entire history is ~9 MB. We rejected a dual-model transition period because it keeps both code shapes alive for months — the exact disease being cured.

## Implementation deltas (2026-07-23)

- History held no semantic opcodes to rewrite — only generic row-ops. The
  rewrite therefore *redirects*: flat games' `games.state_json` row-ops onto
  their match row, `reseed_entries` ops onto `stage_standings`; EK's
  `themes`/`answers` records all sat below every checkpoint (display-only,
  never replayed), so they stay as-is and the converter *guards* that no
  replayable one exists before dropping the tables.
- `games.state_json` survives as EK's seed-import staging (game-level workflow
  state, not protocol state); flat games' copies are blanked after the move.
- Checkpoints are rewritten in place (themes/answers folded into match blobs,
  reseed_entries renamed, flat StateJSON folded into the injected match row).
- The conversion runs at startup inside the trigger-disarmed migration window;
  `scripts/paritygate/paritygate.py` is the scripted rehearsal gate.

## Consequences

- Full `.backup` before cutover; the converter is rehearsed on prod snapshots (`~/dope-prod-snapshots/`) until the gate passes.
- The journal keeps its compactness invariants: log host intents only (edits, overrides, imports as references); everything derivable (resolver fills, computed places, reseed entries, swiss pairings) is recomputed on replay, never logged.
- One new opcode (`OpMatchPatch`: interned match ref + JSON pointer + scalar) covers every current and future protocol; opcodes stay append-only.
- Sequencing: foundation → migrate → parity cutover → new games (brain, individual SI, troika, media) on proven abstractions.
