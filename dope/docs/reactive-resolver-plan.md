# Reactive bracket resolution — implementation plan

> **Status: landed, historical.** The non-destructive resolver and deterministic
> lots (`games.random_seed`) shipped; file paths below predate the 7-group
> reorg (now `dope/domain/resolver/resolver.go`, `dope/server/db.go`). One
> decision was later superseded: `reseed_entries` was NOT kept — the unified
> model generalised it into `stage_standings` (ADR-0001/0004, unified-model.md).

## Problem

A finished EK match was missing one minus. Fixing it required the only available
workflow — untick (set `status='active'`) the match, edit, re-tick — because
editing a finished match is blocked (`db.go` "match is finished"). Unticking made
the resolver treat every downstream slot's source as no-longer-final, so
`applyResolvedSlotTx` **deleted** the downstream protocol (themes/answers/results)
and reopened those bouts. Re-ticking restored the bracket structure but not the
deleted data. The destruction propagated through the whole bracket, including
reseed stages.

Root cause: `resolver.go applyResolvedSlotTx` deletes a slot's protocol data
whenever its resolved occupant changes — including the *transient* change to
"unresolved" (0) during an untick. Reseed instability compounds it: reseed lots
(Жребий) are drawn with `rand` (`freshLot`), so any recompute can reshuffle ties,
and `syncReseedReadinessTx` deletes `reseed_entries` whenever a source bout goes
un-final.

## Goal

Untick → edit → re-tick must lose **no** data and break nothing downstream.
Reseed must not change under tick/untick.

## Design — non-destructive resolver + deterministic reseed (no re-key)

The originally-discussed full re-key (protocol data keyed by slot, `team_id`
demoted to provenance, slot-level staleness flag) is **deferred**, because it
breaks `revertFestToAudit`: revert reconstructs each row's primary-key WHERE
clause from the audited `before_json`, which for historical rows contains
`team_id` and no `slot_id`. Changing the PKs of `themes`/`match_results` would
make every pre-migration audit row un-revertible — silently breaking undo and the
rollback tool. The data loss we must fix lives entirely in the resolver's DELETEs,
so we fix it there with **zero schema-key changes and zero audit-revert risk**.

### 1. Non-destructive slot resolution (`resolver.go applyResolvedSlotTx`)

- **Hold on unresolved**: if `desired == 0` (the upstream source is not currently
  final — e.g. it was unticked to be edited), make **no change**: keep the slot's
  current occupant and all its protocol data. Re-finishing the source restores the
  same slot with no churn (untick→retick becomes a no-op downstream).
- **Never delete protocol data**: remove the `delete from themes …` and
  `delete from match_results …` statements.
- **Genuine occupant change** (`desired != 0 && desired != current`): update
  `match_slots.team_id`, `ensureRegularThemes` for EK, and reopen the bout
  (`status='active'`) so its standings get re-reviewed against the new occupant.
  Reopening is non-destructive (no data deleted); the previous occupant's rows
  remain in the DB.

Net: the only behavior that changes is *re-resolution of an already-occupied
slot* — exactly the buggy path. Normal forward progression (empty slot → first
occupant, `current == 0`) is unchanged.

### 2. Deterministic reseed lots (`resolver.go` + `games.random_seed`)

- Add `games.random_seed` (TEXT), set at game creation and backfilled once for
  existing games (additive column; `games` PK stays `id`, so audit-revert is
  unaffected).
- Replace `freshLot` (`rand`) with a deterministic lot
  `lot = fnv1a(random_seed || ":" || team_id) % 1_000_000 + 1`. Ties now break
  the same way every recompute, so re-finishing a source never reshuffles a
  reseed. The persisted-draw plumbing (`loadReseedDraws`/`prevDraw`) is no longer
  needed (the lot is stable by construction).

### 3. Hold reseed entries under transient unreadiness (`syncReseedReadinessTx`)

- Stop deleting `reseed_entries` when prerequisites go un-final; **hold** them.
  Combined with deterministic lots and the manual "calculate" action (kept as-is),
  reseed never changes under tick/untick. A correction that genuinely changes who
  advances is reflected when the organizer recalculates (same workflow as today).

### Kept as-is (lower risk, no regression)

- `reseed_entries` table is **kept** (as a derived cache) rather than dropped —
  dropping an audited table is unnecessary for the goal and adds audit churn.
  *(Superseded: the unified model later folded it into `stage_standings`.)*
- Manual reseed "calculate" workflow is kept (no auto-recompute-on-edit), avoiding
  a surprise auto-advance behavior change on a live multi-fest system.

## Deferred (need explicit review — out of scope for this safe pass)

- **Slot-keyed storage + provenance + staleness flag** (show entered data on a
  genuine advancement change, red-highlight the mismatch). Requires re-keying
  `themes`/`match_results`, which breaks audit-revert for historical rows unless
  the audit JSON is also backfilled. High blast radius; do under review.
- **Fully reactive reseed** (auto-recompute on every edit, drop manual calculate).

## Touch points

- `dope/resolver.go`: `applyResolvedSlotTx` (hold + no deletes), `assignDrawLots`/
  `freshLot` → deterministic, `recomputeReseedEntriesTx` (pass seed),
  `syncReseedReadinessTx` (hold), seed lookup helper.
- `dope/db.go`: `migrateDB` add `games.random_seed` + one-time backfill; set seed
  on game creation.
- Tests: update `resolver_test.go` expectations (no destruction); add an
  untick→edit→retick no-data-loss test and a reseed-determinism test.

## Verification

- `just pre-commit` (fmt/vet/test + JS).
- Against a copy of the rolled-back prod DB: untick a finished EK bout, edit a
  score, re-tick — assert downstream themes/answers/results and reseed entries are
  unchanged (byte-identical where advancement is unchanged).
- Deploy; backup first.
