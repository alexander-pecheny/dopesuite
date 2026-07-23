# Unified model: Structure × Protocol

Design for rebuilding dope so every game — current (EK, OD/ЧГК, КСИ ± stickers) and planned (brain, individual СИ, troika, media games) — is one composition: a game-agnostic **Structure** whose **Matches** each run a registered **Protocol**. Vocabulary: [`CONTEXT.md`](../CONTEXT.md). Decisions and rejected alternatives: [ADR-0001..0004](adr/). This doc is the implementation blueprint.

## 1. Target schema

Kept as-is: users/auth, fests, `fest_teams` / `fest_players` / rosters, venues, organizers, festaccess, journal tables (vocabulary changes, §4). Changed:

```sql
games      (id, fest_id, code, slug, title, protocol,        -- was game_type
            participant_kind,                                -- 'team' | 'player'
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
            source_type,                                     -- 'seed' | 'from_match' | 'stage_rank' | 'reseed' | 'fixed' | 'placeholder'
            source_ref_json, participant_id, locked)         -- participant_id resolves via games.participant_kind

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

Invariant: **log host intents, never derived state.** Resolver fills, computed places, `stage_standings`, swiss pairings — recomputed on replay, never logged. Auto-places mean former `OpPlace` entries shrink to override-only. One new opcode `OpMatchPatch` (interned match ref + JSON pointer + scalar) is the universal edit record for all protocols forever; `OpFinish`/`OpUnfinish` stay; `OpMark`/`OpPlace`/`OpThemePlayer` are retired (still decodable — opcodes are append-only). Scheme imports log a reference to the versioned `schemes` row, not the document (keeps hot rows tiny; today they average 4.6 KB because of embedded blobs). Checkpoints become the match blobs verbatim. History migration: rewrite into the new vocabulary with a replay-parity gate (ADR-0004).

## 5. Frontend (ADR-0003)

TypeScript, esbuild, one **shell** + per-protocol **renderers**:

- `web/ts/shell/` — game topbar with tabs, breadcrumbs, SSE sync (epoch/seq, delta application), stage navigation, host/viewer split by `CanEdit`, static-mode.
- `web/ts/structure/` — stage-kind views shared by all games: cross-table, bracket tree, reseed panel, standings.
- `web/ts/protocols/<code>/` — implements `ProtocolRenderer`: build match grid, cell editing/keypad wiring, apply state patch. Nothing else; the shell owns the page.
- `web/ts/lib/` — today's `match-table.js` helpers, typed.

`justfile`: `dev` runs esbuild watch; `test` adds `tsc --noEmit` + existing node tests; embed ships built output. `menu.js`/`pageforms.js` chrome stays classic.

## 6. Migration & gates (ADR-0004)

Converter (`storage/migrate`): EK `themes`/`answers` → per-match blobs; OD/KSI game blob → the game's single match; schemes → v3; journal → new vocabulary. Rehearse on `~/dope-prod-snapshots/` until all gates pass, then cut over in a quiet window after a full online `.backup`.

Gates:
1. **Result parity** — converted DB renders byte-identical results/exports/views for every historical fest.
2. **Replay parity** — rewritten journal replayed from genesis equals converted final state.
3. **Perf parity** — edit latency and SSE fan-out no worse than baseline (`scripts/loadtest` before/after on the snapshot).

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
