---
status: accepted
date: 2026-07-24
---

# Protocol edits are state PATCHes; edit intent is not wire vocabulary

Every Protocol edit — EK cells, player attribution, place pins, shootout themes, same as OD/KSI — travels as JSON set-ops against the match's state blob, applied through the one editbatch accumulator (one window, one lock, one transaction, one commit for every write in the system). The EK `/update` command language (`matchedit` action whitelist, server-side player-name resolution, place writes into `match_results`) is deleted, not relocated: the server learns *which paths changed*, never *what the host did*, and recomputes everything semantic (scores, places, bracket advancement) from the resulting state. Structure transitions (`finish`, `venue`) are not Protocol state — they stay command endpoints, routed through the same accumulator as jobs. We rejected sharing only the accumulator while keeping `/update` (leaves two edit vocabularies alive) and a server-side `/update`→ops translation shim (new distinct code, the disease being cured).

## Consequences

- Pins move into the blob as Protocol state; the scorer honours them and `match_results` becomes scorer-write-only (`place_override` rows backfill per-match, streamed — migrations must never slurp whole tables, a prior one OOM-killed prod).
- The journal's semantic event for an EK edit is `game:state-patch` with raw ops; per-game history loses EK verbs ("pinned to place 1") unless derived back from paths.
- Validation is protocol-owned shape-checking (`ValidateOps`) plus the finished-match guard; intent is not validated because intent is no longer expressed.
- The client speaks ids, not names (roster `{id, name}` in the payload); the pending-ops localStorage key is versioned so old-format ops are orphaned, never misapplied. No `/update` compat shim — a stale tab reloads.
