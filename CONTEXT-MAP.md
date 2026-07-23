# Context map

Three bounded contexts, one per Go module. Read the `CONTEXT.md` of whichever you're working in; root `docs/adr/` holds system-wide decisions, `<module>/docs/adr/` module-scoped ones.

- [`dope/CONTEXT.md`](dope/CONTEXT.md) — tournament scoring: Fest, Game, Structure × Protocol, Match, Slot.
- [`xy/CONTEXT.md`](xy/CONTEXT.md) — encrypted ЧГК question-editing boards: Board, Card, 4s, List Group.
- [`dopeuikit/CONTEXT.md`](dopeuikit/CONTEXT.md) — the UI system: Engine, Kit, Overlay, Primitive, Mount.
