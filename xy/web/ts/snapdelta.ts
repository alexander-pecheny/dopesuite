// snapdelta.ts — downloading a board's snapshot as a delta (ADR-0023).
//
// The server numbers every change to a board's cards. A device that already
// holds the board as of some rev asks GET /api/boards/{id}?since=<rev> and gets
// the board's small parts in full, but of the cards only the ones that changed:
// a card whose text changed comes whole, a card that only moved comes as
// {id, list_id, rank, same: true}, and a deleted one is an id in gone_cards.
// The labels, Playings and votes come only for the changed cards.
//
// The delta is merged onto the BASE: the last snapshot exactly as the server
// sent it. It is kept apart from the mirror (sync.ts), because the mirror also
// holds edits made offline that the server may never have accepted, and a
// delta merged onto those would keep them forever.
//
// ES module.

import type { BoardSnapshot, SnapshotCard } from "./store.js";

// One delta's extra fields, on top of a snapshot's.
interface DeltaFields {
  rev?: number;
  since?: number | null;
  gone_cards?: number[];
}
type Delta = BoardSnapshot & DeltaFields;

interface CardRow { card_id: number }

// DeltaMismatch says a delta cannot be merged onto the base this device has.
// The caller then asks for the full snapshot.
export class DeltaMismatch extends Error {
  constructor(msg: string) { super(msg); this.name = "DeltaMismatch"; }
}

// isDelta tells a delta from a full snapshot.
export function isDelta(snap: Delta): boolean {
  return typeof snap.since === "number";
}

// mergeSnapshot applies a delta to the base it was asked against and returns
// the full snapshot the server would have sent. Pure: base is not changed.
export function mergeSnapshot(base: Delta, delta: Delta): BoardSnapshot {
  if (!isDelta(delta)) return delta;
  if (base.rev !== delta.since) throw new DeltaMismatch(`delta since ${delta.since} onto rev ${base.rev}`);
  const changed = new Set<number>(delta.gone_cards || []);
  for (const c of delta.cards || []) changed.add(c.id);
  const had = new Map((base.cards || []).map((c) => [c.id, c]));
  const cards: SnapshotCard[] = (base.cards || []).filter((c) => !changed.has(c.id));
  for (const c of delta.cards || []) {
    if (!c.same) { cards.push(c); continue; }
    const prev = had.get(c.id);
    if (!prev) throw new DeltaMismatch(`delta keeps card ${c.id}, which the base does not have`);
    cards.push({ ...prev, list_id: c.list_id, rank: c.rank });
  }
  const keep = <T extends CardRow>(rows: unknown, fresh: unknown): T[] =>
    ((rows as T[] | undefined) || []).filter((r) => !changed.has(r.card_id)).concat((fresh as T[] | undefined) || []);
  const out: Delta = {
    ...delta,
    cards,
    card_labels: keep(base.card_labels, delta.card_labels),
    card_sessions: keep(base.card_sessions, delta.card_sessions),
    votes: keep(base.votes, delta.votes),
  };
  delete out.since;
  delete out.gone_cards;
  return out;
}

// What fetchSnapshot needs: the network, and where the base is kept. getBase
// resolves undefined when there is none (a first open, or a local xy).
export interface SnapshotIO {
  fetchJSON(url: string): Promise<unknown>;
  getBase(boardId: number): Promise<BoardSnapshot | undefined>;
  putBase(boardId: number, snap: BoardSnapshot): Promise<void>;
}

// fetchSnapshot downloads a board's snapshot, as a delta when this device
// holds a base, and stores the result as the next base. A delta that does not
// fit the base costs one more request, for the full snapshot.
export async function fetchSnapshot(boardId: number, io: SnapshotIO): Promise<BoardSnapshot> {
  const url = `/api/boards/${boardId}`;
  const base = (await io.getBase(boardId).catch(() => undefined)) as Delta | undefined;
  const since = typeof base?.rev === "number" ? base.rev : null;
  const got = (await io.fetchJSON(since === null ? url : `${url}?since=${since}`)) as Delta;
  let snap: BoardSnapshot;
  try {
    snap = base ? mergeSnapshot(base, got) : got;
    if (!base && isDelta(got)) throw new DeltaMismatch("delta without a base");
  } catch (e) {
    if (!(e instanceof DeltaMismatch)) throw e;
    snap = (await io.fetchJSON(url)) as BoardSnapshot;
  }
  await io.putBase(boardId, snap).catch(() => undefined);
  return snap;
}

export const xySnapDelta = { mergeSnapshot, fetchSnapshot, isDelta, DeltaMismatch };
