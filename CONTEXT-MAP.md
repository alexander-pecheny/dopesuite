# Context map

There are four bounded contexts, one for each app and for the UI module.
`dopecore` is a shared platform layer and has no glossary of its own.

The root [`CONTEXT.md`](CONTEXT.md) holds the few terms that mean the same thing
everywhere: Catalog, Surface, String Id and User Error. Read the `CONTEXT.md` of
whichever module you are working in. System-wide decisions are recorded in the
root `docs/adr/`, and decisions that affect only one module in that module's own
`<module>/docs/adr/`.

- [`dope/CONTEXT.md`](dope/CONTEXT.md) — tournament scoring: Fest, Game, Structure × Protocol, Match, Slot.
- [`xy/CONTEXT.md`](xy/CONTEXT.md) — encrypted ЧГК question-editing boards: Board, Card, 4s, List Group.
- [`spliff/CONTEXT.md`](spliff/CONTEXT.md) — shared expenses: Group, Transaction, Payment, Share, Unclaimed, Debt graph.
- [`dopeuikit/CONTEXT.md`](dopeuikit/CONTEXT.md) — the UI system: Engine, Kit, Overlay, Primitive, Mount.
