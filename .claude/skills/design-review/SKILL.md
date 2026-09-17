---
name: design-review
description: Check a new or changed UI surface against the design system before shipping it. Find the primitives that already exist, avoid inventing class names, and look at the result next to the surface it should resemble. Use this after building any panel, modal, bar, row or card in xy or dope, and before you commit frontend work.
---

# Reviewing a surface against the design system

The design system is good and its defaults are sensible. Even so, features keep
coming out looking wrong, and they go wrong in the same few ways every time.
This document lists those ways and the checks that catch them.

Run this **once the thing works, and before you commit it**. It is not a matter
of style opinions: every step below either finds something you could have reused
and did not, or a number that is actually wrong.

## Why this keeps happening: the mount hole

`.dopeui` is a **closed vocabulary and the compiler checks it**. An unknown
primitive or prop is a build error, and that is what keeps the declared pages
consistent with each other.

`mount id="x" kind="y"` is a hole in that. Everything built inside a mount is
hand-written TypeScript, which no compiler and no vocabulary checks at all. xy
has **41 mount kinds**, and nearly every feature body — every panel, every modal
body, every bar — is inside one. So the enforcement that makes the system work is
missing in exactly the place where features actually get built, and you are on
your own precisely where you are least likely to notice.

`scripts/classcheck` does **not** help here. It only checks that a class is
styled if and only if something emits it. A brand-new layout class that you had
no business inventing passes it cleanly. A green classcheck tells you nothing
about whether the class should exist.

## 1. Name the twin, and read it

Before you write a single node, ask: **which existing surface is this one's
twin?** xy already has a modal body full of controls, a bar above the board, and
a row in a list. Open the markup and the CSS of whichever one yours resembles,
and match it.

Twins worth knowing in xy:

- modal body of controls → `.mass-body` (`masspanel.ts`), the sessions form
- a bar over the board → `.mass-bar` (in-flow row of `.board-main`)
- a row in a list → `.member-row`, `.attach-row`, `.invite-row`
- picking labels → `.label-picker` + `.label-pick` chips (`masspanel.ts`,
  `cardlabels.ts`) — do not invent a second label chip
- a segmented choice → the kit's `.seg` / `.seg-btn`
- helper text → `.hint` (and `.hint-danger` for a warning)
- an empty state inside a panel → `.label-empty`

If you cannot name a twin, that is itself the finding. Either you are building
something genuinely new, which is rare and which you should say out loud, or you
have not looked properly.

## 2. Inventory before you invent

Grep before adding any class name:

```bash
grep -n "^\.u-" dopeuikit/assets/core.css              # layout utilities
grep -rn "class: \"" xy/web/ts/<nearest-feature>.ts    # what the twin emits
python3 -c "import json;print(json.load(open('dopeuikit/kit/vocab.json'))['enums'].keys())"
```

**The kit already has layout utilities for exactly this.** They are the thing
people miss most often in this repo:

```
.u-col .u-row .u-wrap .u-grow .u-spacer
.u-gap-xs .u-gap-sm .u-gap-md .u-gap-lg .u-gap-xl
.u-align-start|center|end  .u-justify-center|end|between
```

`firstrun.ts` uses them, as `u-row u-gap-sm u-wrap`. If you find yourself
writing a class whose entire body is `display:flex; gap:…; align-items:…`, you
are re-inventing one of these. Delete it and compose the utilities instead.

## 3. A new class must carry identity, not layout or typography

**Every new class name costs something.** Be able to justify each one.

- Layout (flex, gap, alignment) → `.u-*` utilities. Never a new class.
- Muted helper text → `.hint`. Never a new class.
- Colour, size, weight of ordinary text → an existing token pairing; look at the
  twin before minting `.thing-note { color: var(--muted); font-size: … }`.
- **Identity** is the one good reason to add a class: "this is an invite row",
  "this is the filter bar's own label".

When two surfaces end up with the same box (border, radius, padding, surface),
share the rule rather than copying it: `.mass-bar, .filter-bar { … }`.

## 4. Spacing is the container's job

This is deliberate: **`.hint` has `margin: 0`**, and so do most text
primitives. Children do not space themselves. The container spaces them, with a
`gap` taken from the scale.

Here is the failure this is meant to prevent, which still happens anyway. You
put `margin-bottom: var(--space-2)` on one child. You get 8px in that one place
and **0 everywhere else**, and you ship a body whose items are bunched together
in uneven pairs.

- Container: `display:flex; flex-direction:column; gap: var(--space-N)`, or
  `u-col u-gap-md`.
- Never a margin on a child to create rhythm.
- Use the `--space-*` scale; never a bare `px` for spacing.

Check the numbers. Gaps that are unequal when you did not mean them to be are
the giveaway:

```bash
agent-browser eval --stdin <<'JS'
const kids = [...document.querySelector("#filterBody").children];
JSON.stringify(kids.slice(1).map((n, i) =>
  Math.round(n.getBoundingClientRect().top - kids[i].getBoundingClientRect().bottom)))
JS
# want one repeated number, not [8, 0, 0]
```

## 5. Prefer declaring over constructing

A `mount` is for content that **changes at runtime**, such as the cards of a
list or the rows of a roster. Structure that is static — a caption, a segmented
control, a hint, a button — belongs in the `.dopeui`, where the closed
vocabulary, `col`/`row`/`gap` and the compiler all still apply.

Before you build a body in TypeScript, work out which parts of it never change.
Declare those parts instead.

## 6. Look at it, beside its twin

All the numbers can be right while the surface still looks wrong: overflow is
0, the counts are correct, every element is present, and the thing still looks
bolted on. So **look at it**, and look at it next to something else:

```bash
agent-browser set media dark          # and light
agent-browser set device "iPhone 16"  # and set viewport 1280 800 1
agent-browser screenshot '#thing' $SP/new.png
agent-browser screenshot '#its-twin' $SP/twin.png
```

Look at the two side by side and answer honestly: **could somebody tell which
of these shipped today?** If they could, work out what gives it away — the
rhythm, the alignment, a different corner radius, a heavier label, a margin on
one child — and fix that.

Do this in all four cells, as in the verify skill's matrix: phone and desktop,
light and dark.

## 7. Report what you reconciled

When you hand the work over, say which twin you matched and which primitives
you reused. "Built the body with `u-col u-gap-md`, the chips are the existing
`.label-picker`, the helper text is `.hint`, and the bar shares `.mass-bar`'s
rule" is a design review. "Looks fine" is not.

## The galleries

Both apps have a `/gallery` page that works in dev mode only. dope's is served
from `dope/web/ui/app.go` and draws every shared table and the Сетка from
fixtures on one page, which is why a change to a table skin there can be judged
from four screenshots instead of eighty. xy's is `xy/web/assets/ui/gallery.dopeui`
with `xy/web/ts/gallery.ts` behind it.

Use the gallery for step 6 wherever the surface you changed appears on it. If it
does not, you still have to seed a board and drive to the surface itself, which
is slow — and skipping that step is how surfaces drift apart.
