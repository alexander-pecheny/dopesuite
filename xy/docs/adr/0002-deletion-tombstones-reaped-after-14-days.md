# Deletion is a tombstone, reaped after 14 days

Deleted entities (boards, lists, cards, labels, timeline events, attachments)
used to be soft-deleted forever: rows and attachment blobs stayed, and kept
counting toward the owner's quota. We decided every delete stays a tombstone for
14 days — hidden, quota-free, restorable by un-setting `deleted_at` — and is
then permanently destroyed by a reaper in the server (hourly ticker, the
`staging.go` reapLoop pattern; also invocable ad hoc as `xy-server gc`). Boards
reap via `DELETE FROM boards` + FK cascade; blobs are removed as their rows die,
plus an age-thresholded orphan sweep. 14 days matches the litestream snapshot
retention, so the recovery window is uniform: within it, restore is a SQL
un-tombstone; past it, the data is gone everywhere.

## Consequences

- The per-attachment delete endpoint no longer removes the blob immediately —
  the blob lives until the tombstone reaps.
- The blob backup timer flips from `rclone copy` (append-only, the README's old
  rationale) to `rclone sync --backup-dir` into a dated R2 trash prefix, pruned
  by the same timer after 14 days (by the date in the prefix name — `--min-age`
  filters on object mtime, which R2 preserves from the original upload, so it
  would prune every reaper-deleted blob immediately; the R2 keys
  are object-scoped, so a bucket lifecycle rule wasn't settable). Physical
  erasure everywhere is therefore worst-case ~28 days (14 tombstone + 14 trash).
- Quota SQL counts live data only: it must exclude tombstoned boards' content
  and tombstoned cards' attachments/timeline.
- Board delete becomes eventually irreversible, so its dialog requires typing
  the board name.
- Carve-out: comment deletion blanks `payload_enc` at delete time (pre-existing,
  deliberate — deleting a comment removes its text at once), so a restored
  comment row has no content. Everything else restores whole within the window.
