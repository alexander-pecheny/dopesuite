# fest-grid: a pure planner, then a painter (#10)

Status: open. Strength: speculative — do it when the Сетка next changes for
another reason (most likely [setka-one-skin.md](setka-one-skin.md)), not on
its own.

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
