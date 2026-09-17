# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT-MAP.md`** at the repo root. It points at one `CONTEXT.md` per module. Read whichever ones are relevant to the topic.
- **`docs/adr/`** at the root for system-wide decisions, and `<module>/docs/adr/` for module-scoped ones.

If any of these files do not exist, **carry on without saying anything**. Don't point out that they are missing, and don't suggest creating them in advance. The `/domain-modeling` skill, which you reach through `/grill-with-docs` and `/improve-codebase-architecture`, creates them as and when terms or decisions actually get settled.

## File structure

This repo has several contexts in it. Three of them are bounded contexts:
dopeuikit, xy and dope. `dopecore` is a shared platform layer and has no
glossary of its own.

```
/
├── CONTEXT-MAP.md
├── docs/adr/            ← system-wide decisions
├── dopeuikit/
│   ├── CONTEXT.md
│   └── docs/adr/
├── dopecore/            ← platform layer, no CONTEXT.md
├── xy/
│   ├── CONTEXT.md
│   └── docs/adr/
└── dope/
    ├── CONTEXT.md
    └── docs/adr/
```

## Use the glossary's vocabulary

Whenever what you write names a domain concept — in an issue title, a refactoring proposal, a hypothesis, a test name — use the term as the relevant `CONTEXT.md` defines it. Don't slide into a synonym that the glossary explicitly tells you to avoid.

If the concept you need is not in the glossary at all, stop and work out which of two things is happening. Either you are inventing language the project does not use, in which case reconsider, or there is a genuine gap, in which case note it for `/domain-modeling`.

## Flag ADR conflicts

If what you are proposing contradicts an existing ADR, say so plainly instead of quietly overriding it:

> _Contradicts ADR-0001 (unified frontend toolchain) — but worth reopening because…_
