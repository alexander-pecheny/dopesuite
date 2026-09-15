# Context map

Four bounded contexts, one per app/UI module (`dopecore` is a shared platform layer with no domain glossary). Root [`CONTEXT.md`](CONTEXT.md) holds the few terms that hold across all three: Catalog, Surface, String Id, User Error. Read the `CONTEXT.md` of whichever you're working in; root `docs/adr/` holds system-wide decisions, `<module>/docs/adr/` module-scoped ones.

- [`dope/CONTEXT.md`](dope/CONTEXT.md) — tournament scoring: Fest, Game, Structure × Protocol, Match, Slot.
- [`xy/CONTEXT.md`](xy/CONTEXT.md) — encrypted ЧГК question-editing boards: Board, Card, 4s, List Group.
- [`spliff/CONTEXT.md`](spliff/CONTEXT.md) — shared expenses: Group, Transaction, Payment, Share, Unclaimed, Debt graph.
- [`dopeuikit/CONTEXT.md`](dopeuikit/CONTEXT.md) — the UI system: Engine, Kit, Overlay, Primitive, Mount.
