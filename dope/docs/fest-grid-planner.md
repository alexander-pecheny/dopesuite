# fest-grid: a pure planner, then a painter (#10)

Status: done 17 Aug 2026 (see «What was done» at the end). Written as the
hand-off note; the strength was speculative, and it was done last, after #7
had made the group table a бой box.

## The problem

`web/ts/fest-grid.ts` builds DOM, then settles rows, then measures in a
rAF, with file-scope state between the steps: `placed`/`blocks` (`:582`),
`boutLetters` (`:805`, read by `buildReseedStagePanel` outside any build),
`activeFestGridRoot` (`:99`). Two grids on one page would corrupt each
other (the brain page draws the Сетка and a pod board from the same
module), and the packing rule — `columnsFor`, `layoutBlockColumns`
(`:270–300`), `settleRows` (`:590`) — is testable only through drawn markup
in a DOM stub that has no layout.

## The change

```ts
planGrid(stages: FestGridStage[], opts: {viewportRows?: number}): GridPlan
// GridPlan: unitRows, per section {kind: "matches" | "block" | "standings",
//           items: [{code, rows, units, row?}], blockRows, blockCols}
paintGrid(plan: GridPlan, data, options): HTMLElement
```

`planGrid` is pure and owns unit rows, spans, the block packing and the
letters (as a `Map` passed in, not module state); `paintGrid` is the DOM.
`layoutBlockColumns` becomes "measure the viewport rows, re-plan, re-paint
the block shape". Return a builder instance (`createFestGrid()`) rather
than module-level `let`s so two grids coexist.

## Acceptance

- `deno test` asserts the packing rule (12 групп at five to a column read
  4+4+4; a Block of one Group stands alone; a nine-row group spans two
  units) on `planGrid` output, with no DOM.
- The brain page draws its Сетка and a pod board without either touching
  the other's letters or rows.
- Screenshots of ЭК, СИ, ТПШ, КИнСБФ grids identical to before (verify
  matrix, `imgdiff.py`).

## Traps

- `--grid-unit` in CSS is `unitRows × --grid-row + gaps`; the plan's
  `unitRows` must still land on `.fest-grid` as `--grid-unit-rows`, or the
  Block stack and the бой columns fall off each other (the 16 Aug bug).
- `buildReseedStagePanel` is exported and called from `ek.ts` for the
  Пересев tab; keep its letters an explicit argument (it already takes
  `{letters}`).

## What was done (17 Aug 2026, branch `dope-refactor`)

- `planGrid(stages, liveStages, {viewportRows?}) → GridPlan` is pure and
  owns what the note listed: which column each stage is (`matches`,
  `standings`, or a `block` of Groups), the shared row unit (`unitRows`),
  every box's `rows` and `units` (a `GridItem`), a table's rows and sort, and
  each Block's `rows × cols` shape from `packBlock(units, viewportRows)` —
  the packing rule alone, exported. The painters (`buildBlockColumn`,
  `buildStandingsStage`, `buildMatchesStage`, `buildStandingsTable`,
  `buildMatchBox`) read the plan and add nothing: a box's seat count is
  `item.rows - 1`, its span `spanRows(box, item)`.
- No module state. The letters are paint, not layout, so they ride in a
  `PaintContext` (letters, options) down the builders rather than on the
  plan; `buildReseedStagePanel` takes its letters from its options only.
  Each drawn grid is a `Grid` (root, its Blocks with their units, its update
  frame) in a module `Set`; a build and a resize each drop the grids whose
  root has left the page (ЭК rebuilds its Сетка on every revision), and the
  resize re-shapes the rest. `layoutBlockColumns` is «measure the rows that
  fit, `packBlock`, repaint the shape» — the note's sentence.
- Not a `createFestGrid()` builder instance, and no separate `paintGrid`:
  with no module `let`s left, the registry entry per root is what the
  instance would have held, and `buildFestGrid` paints the plan inline.
- One visible rule made uniform: a Group the Ranker has not written yet
  draws bare (name and М) in a lone column as it always did inside a Block;
  it used to carry an empty metric column there.
- `--grid-unit-rows` still lands on `.fest-grid`, from the plan, before the
  columns are built.
- Acceptance: `deno test` asserts the packing rule on `packBlock`/`planGrid`
  output with no DOM (12 групп at five to a column read 4+4+4; a Block of one
  Group stands alone; a nine-row group spans two units), and that two grids
  keep their own rows and letters; the verify matrix is 88/88 identical.
