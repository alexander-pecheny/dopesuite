---
status: accepted
date: 2026-07-30
---

# Colour is a rung on the uchu ladder, and a theme is a window on it

The Kit's colour vocabulary is uchu (uchu.style), vendored whole as OKLCH triples in `palette/uchu.json` and emitted by `cmd/palettegen` into every language that needs it: the `--uchu-*` custom properties in `core.css`, the swatch enums in Go, the picker arrays in TypeScript. A role names a **rung** — `(variant, hue, 1..9)` — and never a hex, so a theme is the set of rungs its roles point at rather than a palette of its own. Light walks `gray` in single steps with ink off `yin`; dark reads up `yin`; high contrast is a **stride**, taking two rungs at a time where the regular ladder is tight. The four theme blocks used to pick 73 neutral hex values by hand, and because each was chosen in isolation the ladder was not monotone in any of them — two dark pairs sat 0.003 and 0.007 apart, and light held eleven near-white surfaces inside a single 0.10 band. Rungs cannot land that close. We rejected keeping hand-picked hexes with a linter over them (the values were never the problem — their independence was), and generating a bespoke OKLCH ramp of our own (uchu is already designed, already balanced, and already what the Board Label palette used).

The ladder's properties are asserted rather than trusted: `palette/palette_test.go` reads `core.css` back and checks monotonicity, a ΔL 0.03 floor between adjacent surfaces, WCAG AA for every ink-on-fill pair the Kit composes, and that high contrast never compresses the ladder. Writing those tests is what surfaced two live bugs — light's `--action-bg` moved the *wrong way* in high contrast, and `--header`/`--toolbar` were declared in the dark high-contrast block and styled nothing in either app.

## Consequences

- Adding a surface means picking a rung, not a colour. A role that wants a shade the ladder does not have wants a different rung; a neutral written as a hex fails `TestNeutralRolesNameARung`.
- Re-theming is arithmetic. "Dark's controls are too light" is an edit to one integer, not a fresh trip to a colour picker for four values that then have to be checked against each other.
- Dark's ladder is capped at five surfaces because `yin` offers exactly five rungs between L 0.14 and 0.53. That constraint is the feature; the ten it replaced included pairs nobody could tell apart.
- `yin`'s steps *narrow* toward its light end (0.099 at yin-9, 0.077 by yin-3), so dark high contrast strengthens lines and ink instead of stretching fills — stretching would compress the ladder and push fills toward an ink that mode brightens to yang.
- Derived tokens replace restated ones: the `-transparent` twins, the ghost washes and every shadow are washes *of* the role they belong to, so dark restates none of them.
- A *set* a user picks from is a window too, and the Board Labels became one: a label stores a name, and each theme resolves it to the most chromatic rung of that hue clearing 3:1 against the card it is drawn on. It has to work that way — the band clearing 3:1 on a near-white card and the band clearing it on a mid-grey one do not overlap, so no stored hex is legible in both. The set left uchu's eight hues at the same time (`palette/sets.json` derives teal, cyan, lime, magenta by interpolating adjacent ramps, and brown by draining orange), which is a new ramp rather than a new hex: still nine rungs, still arithmetic.
- The same set is offered in two **registers** — the rung the floor picks, and that rung with a third of its lightness taken away. The deeper one is below the floor on purpose and is held to a perceptual distance from the card instead, because WCAG's ratio reads luminance alone and scores a saturated purple on a mid grey 1.4:1 when you can plainly see it. A ratio is the right tool for ink on a fill and the wrong one for a coloured mark on a coloured ground; grey, having no chroma to be seen by, is the one entry with no second register.

## Three exemptions, and why they are not drift

A reduction pass will reach these and be wrong about all three.

- **The answer-cell pair.** `--green` and `--red` carry right and wrong in the `od` Protocol's question grid, and the high-contrast block separates them in *luminance as well as hue* — dark green against light coral — so they survive red-green colour blindness. This is the one place in the suite where a colour **is** the data, and the constraint is stronger than "pick a rung".
- **The Экран defaults.** `--screen-bg-default` and its siblings are pure white and black on purpose: a projector board is not themed, and the host overrides them per Game. Routing them through the ladder would break that.
- **The sticker palette.** Seven colours, still literal hexes in `palette/sets.json`. `host_games.go` submits the hex with the form and matches the stored value with `EqualFold`, so every existing game's config names one of those strings — repainting them onto rungs orphans those selections and needs a migration. The seam is in place; the repaint is a separate change.
