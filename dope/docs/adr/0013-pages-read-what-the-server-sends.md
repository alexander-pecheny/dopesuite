---
status: accepted
date: 2026-08-17
---

# A page reads what the server sends and derives nothing of its own

The game pages are one module each over the same fest view: ЭК is
`web/ts/ek.ts` for host and spectator alike, gated by a `viewer` flag from
the URL prefix; the tab strip of ЭК, брейн, КСИ and ЧГК comes from one call,
`gameTabs(stages, {game, viewer, seeded})` in `game-tabs.ts`, whose Blocks
come off `grain`, whose круг tabs span a Block's Groups, and whose
`blockLabel`/`groupLabel` are the only readers of «Группа N»; the Сетка is
`planGrid` first — layout as data: what each column is, the shared row unit,
each box's span, a Block's packing — and painters that read the plan, with no
module state, so two grids on a page coexist.

Items #4, #6 and #10 of the 15 Aug 2026 review (`0bcf14a`, `7bf4f85`,
`4365b1b`).

## Considered options

Two ЭК pages (host, spectator) diverged on every feature; four pages each
derived their tabs from stage codes with string tests (`s1-g`, `-reseed`),
and the brain page did it a fourth way; the Сетка kept `placed`, `blocks`
and the буквы in module-level `let`s, so a second grid corrupted the first.
Emitting tab labels from the compiler was considered and kept as an option —
the client already has titles and grain, so the server sends nothing new for
now.

## Consequences

- A new game page calls `gameTabs` and renders the array; a page that tests a
  stage code for `-reseed` or `-g` is a regression. Legacy bookmarks resolve
  through `canonicalKey`.
- `planGrid`/`packBlock` are pure and tested without a DOM (twelve групп at
  five to a column read 4+4+4; a group of nine spans two units); the painters
  take a `PaintContext` (letters, options) and never a module variable.
  `buildReseedStagePanel` takes its letters explicitly.
- Every control on a game page is gated by `viewer`; there is no second page
  to keep in step.
- The verify matrix (`just matrix`, HEAD against the working tree, 88 pages,
  pixel-diffed) is the gate for any change here.
