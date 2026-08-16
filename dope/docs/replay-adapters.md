# Replay harness: a Structure-level adapter, coordinates on the interface, one codec per Protocol (#8)

Status: open. Strength: worth exploring. #1 (Rankers) and #2 (`gamebuild`)
are done, which is what makes the fast adapter buildable. ADR-0010 makes
the replay the conformance gate; this note is about making the gate cheap
and the seam real, not about what it checks.

## The problem

`domain/replay.Game` (`run.go:18`: Seat, Seats, Play, Pin, Finish, Outcome;
plus `LineupWriter`, `StatsReader`) is a deep interface with one real
adapter, `serverGame` in `server/tests/replaydriver_test.go` (805 lines):
it boots the whole server and drives it over HTTP with a session token.
On 16 Aug the four (`TestReplayStudchr{EK,SI,TPSh,Brain}`) took 250 s, of
which 150 s was the edit batcher's 150 ms window awaited once per edit;
`Server.SetEditBatchWindow(time.Millisecond)` in the replay took them to
94 s, and `just test` now skips them (`-short`), `just test-full` plays
them. What is left is CPU: `editbatch.recomputeTouched` resolves the whole
game after every edit (`resolveGameSlotsWithReseedModeTx`, then
`writeStandingsTx` and `si.WriteResultsTx`) — 74 % of the profile — and a
third of that is SQLite parsing unprepared statements. Consequences of the
one-adapter design:

- The coordinate → бой lookup is SQL in the driver (`matchAt`, `:43`) —
  `run.go` cannot say "this Block's standings" without the driver.
- The driver re-derives scoring to report stats (`ekStats`, `brainStats`,
  `individualStats`, `:554–720`), so a change to a Protocol's stat means
  a change to the test harness too.
- A new бой shape edits three places: `replay/parse.go:535` `parseSeat`
  (per-game seat forms by string), the driver's `Play` (`:157`), and the
  Python emitter (`scripts/studchr/emit-*.py`, one per tournament).
- `run.go:50` `statColumns` switches on the game name.
- `replay/report.go` `Discrepancies` has tests and no caller: the documented
  page is never generated. It passes the deletion test today.
- `server/testapi.go` carries ~32 exported aliases so the driver can reach
  in; that is the "one seam" costing more than it returns.

## The change

1. A second adapter, `replay/structgame` (or `domain/replay/adapter`), over
   `gamebuild.Create` + the resolver + the scorer directly, on an in-memory
   SQLite: same `Game` interface, seconds per replay. The HTTP driver stays
   for one test per game type (authorisation and the write-tx path are what
   it proves).
2. `Locate(coord) (matchID, code)` and `Standings(block) []Ranked` join the
   interface, so `run.go` asserts Blocks and coordinates without SQL, and a
   transcript can carry a `[таблица s1/g3]` section checked against the
   server's own `stage_standings` (the tables #1 made the server own).
3. One transcript codec per Protocol: `parseSeat` / render seat / Σ / the
   three stat columns move behind `protocol.Protocol` (or a sibling
   `replay.Codec` registry keyed by Protocol code), so `parse.go` and
   `statColumns` stop switching on strings; the Python emitters become
   thin.
4. Wire `report.go` into `TestReplayStudchr*` output (write the
   discrepancies page under `testdata/studchr2026/`) or delete it.

## Acceptance

- `go test ./dope/domain/replay/... -run Studchr` replays all four studchr
  transcripts through the structure adapter in under 30 s (from 94 s), so
  the gate can leave `-short` and run on every `just test`.
- `server/tests` keeps one HTTP replay per game type; `testapi.go` shrinks
  to what those need.
- Two adapters pass the same `replay.Game` contract test.
- No `game == "brain"` in `parse.go` or `run.go`; the per-Protocol codec
  owns the seat form.
- `report.go` has a caller or is gone.

## Traps

- The HTTP path applies edits through `editbatch` and the write-tx canary;
  the structure adapter must call the same `resolver.ResolveGameSlotsTx`
  and `scoring` entry points, not re-implement them — the point is a
  cheaper transport, not a second engine.
- Reseeds: the driver calls `calculateReadyReseeds` (`:342`) between rounds
  because a host clicks «Рассчитать»; the fast adapter needs the same step
  or the play-off seats stay empty.
- The brain transcript's coordinates changed on 16 Aug (bronze and final are
  one Round, `[s5/r2/w1/m1..m4]`); `emit-brain.py` `FINAL` matches.
- Keep the transcript format (`docs/replay-transcript.md`) stable; it is
  the artefact the sheets are reconciled to.
