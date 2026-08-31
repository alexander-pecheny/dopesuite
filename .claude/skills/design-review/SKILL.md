---
name: design-review
description: Reconcile a new or changed UI surface with the design system before shipping it — find the primitives that already exist, spend no new class names, and look at the result beside the thing it should resemble. Use after building any panel, modal, bar, row or card in xy or dope, and before committing frontend work.
---

# Reviewing a surface against the design system

The system is good and the defaults are sane. Features still come out looking
off, and they come out off in the same few ways every time. This is the list of
those ways and the checks that catch them.

Run this **after the thing works and before you commit it**. It is not a style
opinion pass; every step below either finds a concrete reuse you missed or a
number that is wrong.

## Why this keeps happening: the mount hole

`.dopeui` is a **closed, compile-checked vocabulary** — an unknown primitive or
prop is a build error, and that is what keeps the declared pages coherent.

But `mount id="x" kind="y"` is a hole in it. Everything built inside a mount is
hand-written TypeScript that no compiler and no vocabulary checks. xy has **41
mount kinds**. Practically every feature body — every panel, every modal body,
every bar — lives in that hole. So the enforcement that makes the system work is
absent exactly where features get built, and you are on your own precisely where
you are least likely to notice.

`scripts/classcheck` does **not** cover this. It checks referential integrity —
a class is styled ⟺ something emits it — so a brand-new layout class you had no
business inventing passes clean. A green classcheck says nothing about whether
the class should exist.

## 1. Name the twin, and read it

Before writing a node: **which existing surface is this one's twin?** A modal
body of controls, a bar over the board, a row in a list — xy has one of each
already. Open its markup and its CSS and match it.

Twins worth knowing in xy:

- modal body of controls → `.mass-body` (`masspanel.ts`), the sessions form
- a bar over the board → `.mass-bar` (in-flow row of `.board-main`)
- a row in a list → `.member-row`, `.attach-row`, `.invite-row`
- picking labels → `.label-picker` + `.label-pick` chips (`masspanel.ts`,
  `cardlabels.ts`) — do not invent a second label chip
- a segmented choice → the kit's `.seg` / `.seg-btn`
- helper text → `.hint` (and `.hint-danger` for a warning)
- an empty state inside a panel → `.label-empty`

If you cannot name the twin, that is the finding: either you are building
something genuinely new (rare, say so out loud) or you have not looked.

## 2. Inventory before you invent

Grep before adding any class name:

```bash
grep -n "^\.u-" dopeuikit/assets/core.css              # layout utilities
grep -rn "class: \"" xy/web/ts/<nearest-feature>.ts    # what the twin emits
python3 -c "import json;print(json.load(open('dopeuikit/kit/vocab.json'))['enums'].keys())"
```

**The kit ships layout utilities for exactly this**, and they are the single most
missed thing in the repo:

```
.u-col .u-row .u-wrap .u-grow .u-spacer
.u-gap-xs .u-gap-sm .u-gap-md .u-gap-lg .u-gap-xl
.u-align-start|center|end  .u-justify-center|end|between
```

`firstrun.ts` uses them (`u-row u-gap-sm u-wrap`). If you are writing a class
whose whole body is `display:flex; gap:…; align-items:…`, you are re-inventing
one of these. Delete it and compose utilities.

## 3. A new class must carry identity, not layout or typography

Budget: **a new class name is a cost.** Justify each one.

- Layout (flex, gap, alignment) → `.u-*` utilities. Never a new class.
- Muted helper text → `.hint`. Never a new class.
- Colour, size, weight of ordinary text → an existing token pairing; look at the
  twin before minting `.thing-note { color: var(--muted); font-size: … }`.
- **Identity** — "this is an invite row", "this is the filter bar's own label" —
  is the legitimate reason to add one.

When two surfaces end up with the same box (border, radius, padding, surface),
share the rule rather than copying it: `.mass-bar, .filter-bar { … }`.

## 4. Spacing is the container's job

The system's stance, and it is deliberate: **`.hint` carries `margin: 0`.** So do
most text primitives. Children do not space themselves; the container gives them
a `gap` from the scale.

The failure mode this exists to prevent, and which keeps happening anyway: you
put `margin-bottom: var(--space-2)` on one child, get 8px in that one place and
**0 everywhere else**, and ship a body whose items are jammed in unequal pairs.

- Container: `display:flex; flex-direction:column; gap: var(--space-N)`, or
  `u-col u-gap-md`.
- Never a margin on a child to create rhythm.
- Use the `--space-*` scale; never a bare `px` for spacing.

Check it numerically — unequal gaps you did not intend are the tell:

```bash
agent-browser eval --stdin <<'JS'
const kids = [...document.querySelector("#filterBody").children];
JSON.stringify(kids.slice(1).map((n, i) =>
  Math.round(n.getBoundingClientRect().top - kids[i].getBoundingClientRect().bottom)))
JS
# want one repeated number, not [8, 0, 0]
```

## 5. Prefer declaring over constructing

A `mount` is for content that **varies at runtime** — the cards of a list, the
rows of a roster. Static structure (a caption, a segmented control, a hint, a
button) belongs in the `.dopeui`, where the closed vocabulary, `col`/`row`/`gap`
and the compiler all apply.

Before building a body in TS, ask which parts of it never change. Those parts
should be declared.

## 6. Look at it, beside its twin

Numbers pass while a surface looks wrong — overflow 0, counts right, elements
present, and the thing still reads as bolted on. So **look**, and look
comparatively:

```bash
agent-browser set media dark          # and light
agent-browser set device "iPhone 16"  # and set viewport 1280 800 1
agent-browser screenshot '#thing' $SP/new.png
agent-browser screenshot '#its-twin' $SP/twin.png
```

Read the two together and answer honestly: **could a reader tell which of these
shipped today?** If yes, name what gives it away — rhythm, alignment, a
different corner radius, a heavier label, a lonely margin — and fix that.

Four cells, as in the verify skill's matrix: phone × desktop, light × dark.

## 7. Report what you reconciled

In the hand-over, say which twin you matched and which primitives you reused.
"Built the body with `u-col u-gap-md`, chips are the existing `.label-picker`,
helper text is `.hint`, the bar shares `.mass-bar`'s rule" is a design review.
"Looks fine" is not.

## Known gap

xy has no `/gallery`. dope has one (dev-only, `dope/web/ui/app.go`) that renders
every shared table and the Сетка from fixtures on one page, and it is why a
table-skin change there can be judged in four screenshots instead of eighty. xy's
primitives can only be seen by seeding a board and driving to each surface, which
is slow enough that it does not get done. Building the xy equivalent would make
step 6 cheap; until it exists, step 6 costs a seeded board, and skipping it is
how surfaces drift.
