# Unified model: Structure × Protocol

Design for rebuilding dope so every game — current (EK, OD/ЧГК, КСИ ± stickers) and planned (brain, individual СИ, troika, media games) — is one composition: a game-agnostic **Structure** whose **Matches** each run a registered **Protocol**. Vocabulary: [`CONTEXT.md`](../CONTEXT.md). Decisions and rejected alternatives: [ADR-0001..0004](adr/). This doc is the implementation blueprint.

## 1. Target schema

Kept as-is: users/auth, fests, `fest_teams` / `fest_players` / rosters, venues, organizers, festaccess, journal tables (vocabulary changes, §4). Changed:

```sql
games      (id, fest_id, code, slug, title,
            game_type,                                       -- the Protocol code (kept name)
            participant_kind,                                -- 'team' | 'player'
            state_json,                                      -- game-level auxiliary state (EK seed-import staging ONLY)
            scheme_id, scheme_json, status, position,
            team_list_source, roster_source, random_seed,
            screen_settings_json, revision, ...)

stages     (id, fest_id, game_id, code, title,
            kind,                                            -- registered StageKind: 'rr' | 'se' | 'reseed' | ...
            position, status, config_json)

matches    (id, fest_id, game_id, stage_id, code, title, position,
            participant_count, venue_id, status, revision,
            state_json)                                      -- the Protocol blob (ADR-0002)

match_slots(id, match_id, slot_index,
            source_type,                                     -- 'seed' | 'from_match' | 'reseed' | 'placeholder'
            source_ref_json, team_id, player_id, locked)     -- occupant column picked by games.participant_kind
            -- 'reseed' {stage, rank} is the universal rank reference: it reads
            -- stage_standings whatever kind ranked the stage (a dedicated
            -- 'stage_rank' source proved unnecessary).

match_results(match_id, participant_id, place, place_override,
            total, metrics_json)                             -- written only by the scorer + override path

stage_standings(stage_id, rank, participant_id, metrics_json) -- generalizes reseed_entries to every ranking stage
```

Dropped: `themes`, `answers` (EK state moves into `matches.state_json`), `reseed_entries` (subsumed by `stage_standings`), EK-specific columns that duplicated protocol state. `teams`/`players` game-scoped copies stay only as long as `team_list_source='game'` needs them.

New-primitive rule: a Stage Kind or Protocol is Go code + config vocabulary — **no schema change ever** (ADR-0001/0002).

## 2. Go contracts

```go
// domain/protocol — registry, one per format.
type Protocol interface {
    Code() string                                  // 'ek' | 'od' | 'ksi' | 'brain' | ...
    EmptyState(cfg MatchConfig) json.RawMessage
    Validate(state json.RawMessage, patch Patch) error
    Score(state json.RawMessage) []SlotOutcome     // place + metrics per slot; pure
}

// domain/structure — registry, one per primitive.
type StageKind interface {
    Code() string                                  // 'rr' | 'se' | 'reseed' | 'swiss' | ...
    Schedule(cfg StageConfig, results []MatchOutcome) []PlannedMatch // full upfront or incremental (swiss); hand-authored pairings pass through
    Standings(cfg StageConfig, results []MatchOutcome) []RankedEntry // points rule (2/1/0 vs sum) comes from cfg, not the protocol
}
```

The resolver generalizes to: after any match recompute → `Score` → effective places (override wins) → `Standings` for its stage → fill downstream `match_slots` (`from_match` place / `stage_rank` / `reseed` sources) → `Schedule` any stage that can now extend (swiss) → broadcast affected matches. Deterministic lots (`games.random_seed`) break true ties as today. Tiebreaks *inside* a match (ЧГК mini-tours, brain extra questions, EK shootout) are protocol state; the Structure only sees final places.

## 3. Scheme document

`FestScheme` v3: stages carry `kind` + kind-owned `config`; slots gain `stage_rank` and rename `team` → `fixed` (participant-kind-agnostic). Hand-authored match lists remain valid for any kind (partial round-robins are schedule *data*). Parameterized generators (Go, registered) emit scheme documents for common shapes — "4×RR(4) → reseed → SE(8)"; the document stays the source of truth, importable and diffable, with a visual builder as a later layer.

## 4. Journal

Invariant: **log host intents, never derived state.** Resolver fills, computed places, `stage_standings`, swiss pairings — recomputed on replay, never logged (the row triggers stay disarmed for derived writes via the conversion window; stage standings are written outside trigger scope only during conversion, and live recomputes are idempotent row churn zstd folds away). One opcode `OpMatchPatch` (22, `MPATCH`) is the universal edit record: a match id plus ordered pointer ops (`set` / `remove` / `ensure` / `replace`, `store.BlobOp`) applied to `matches.state_json`; the payload is the same JSON bytes hot and cold. The matches row trigger deliberately ignores `state_json`, so a state edit journals exactly once, semantically (~40 bytes hot instead of the old full-blob row-op averaging 4.6 KB). Replay (`domain/core/revert.go`) applies MPATCH via the tolerant pointer engine in `storage/journal/matchpatch.go`, and blobs encode canonically so live writes and replays are byte-identical.

## 5. Frontend (ADR-0003)

TypeScript, esbuild, one **shell** + per-protocol **renderers**. As landed:

- `web/ts/shell/contracts.ts` — the typed seams: `GameInitPayload`,
  `StateSync`, `ProtocolRenderer`, `RendererRegistry`.
- `web/ts/shell/shell.ts` — the shell (`window.DopeShell`): init parsing,
  renderer registry, state-sync wiring.
- `web/ts/pages/{od,si,host,viewer}.ts` — the page entries: shell first, then
  the legacy page scripts as side-effect imports in their historical load
  order. Each page loads exactly one `dist/<page>.js` bundle (declared in its
  `.dopeui`). This is the migration seam: porting a page means replacing its
  side-effect imports with a registered `ProtocolRenderer`.
- `web/ts/structure/`, `web/ts/protocols/<code>/` — arrive as pages port.

`just build-web` = `tsc --noEmit` + esbuild (gitignored `dist/` output,
embedded at go-build time); `just test`/`deploy` depend on it;
`just watch-web` is the dev loop. `menu.js`/`pageforms.js` chrome stays
classic. Cross-file globals inside a bundle must be explicit `window.*`
exports (module scope no longer leaks top-level functions — see fest-grid.js).

## 6. Migration & gates (ADR-0004)

Converter (`storage/migrate/unify.go`, runs at startup in the trigger-disarmed
migration window): EK `themes`/`answers` → per-match blobs, then dropped; each
flat game gains its `main` stage+match and its state moves there
(`games.state_json` survives only as EK's seed-import staging);
`reseed_entries` folds into `stage_standings`; journal records redirect
mechanically (no simulation — a guard refuses to convert if any replayable
record still references a dropped table); checkpoints are rewritten in place.

Gates — `scripts/paritygate/paritygate.py <snapshot>` runs 1–2 against a
snapshot copy using the working tree's server:
1. **Result parity** — converted storage reproduces every match/game state
   canonically byte-identical.
2. **Journal integrity** — record count unchanged; no replayable record
   references a dropped table; checkpoints decode clean.
3. **Perf parity** — edit latency and SSE fan-out no worse than baseline
   (`scripts/loadtest` before/after, run manually at cutover).

## 7. Build order

Foundation (schema + registries + resolver + shell) → migrate EK/OD/KSI (the parity gates are the proof the abstractions fit) → brain → individual СИ → troika → media. Each new game starts from a spec in `specs/` with the reference UI (Google-Sheet screenshots) attached — protocol rules, state schema, renderer sketch — before implementation.

| Game | Structure | Protocol notes |
|---|---|---|
| ЧГК/OD | 1 stage, 1 match | existing state shape, tours + tiebreak mini-tours |
| КСИ ± stickers | 1 stage, 1 match | stickers = protocol config, not a separate type |
| EK | groups + reseed + SE | 12 themes / fielded players / shootout |
| Brain | RR groups → reseed → SE | K questions, buzzer player, 1/0, tiebreak questions |
| Individual СИ | any; `participant_kind='player'` | СИ buzzer scoring per player |
| Troika | bracket of matches | themes of 3+; three individual answers × value |
| Media | 1 stage, 1 match | rounds as config sections, per-answer values, one sheet UI |
