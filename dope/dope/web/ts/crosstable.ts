// The группа cross-table every two-seat format draws: who met whom and what
// the бой finished, with the block's own standings columns beside it. Брейн
// and Тройка both read it; a format that adds a column names the metric it
// wants rather than restating the table.

import {formatDisplayText, td} from "./cells.js";
import {standingsTable} from "./standings.js";

// A seat ref as a scheme writes it, before anyone sits in it.
export interface SchemeSlotRef {
  label?: string;
  seed?: {number?: number; position?: number} | null;
  reseed?: {stage?: string; rank?: number} | null;
}

// slotKey is a stable identity for an entrant ref, whatever grain it is — what
// pairs a planned бой with the two rows it belongs to.
export function slotKey(slot: SchemeSlotRef | null | undefined): string {
  if (!slot) return "";
  if (slot.seed?.number) return `s${slot.seed.number}`;
  if (slot.seed?.position) return `p${slot.seed.position}`;
  if (slot.reseed) return `r${slot.reseed.stage || ""}:${slot.reseed.rank || 0}`;
  return slot.label || "";
}

// crossSlot is a planned seat as a row: its key and the label the Сетка prints.
export function crossSlot(slot: SchemeSlotRef | null | undefined): CrossSlot {
  return {key: slotKey(slot), label: slot?.label || ""};
}

// standingsByParticipant is a stage's own table keyed by Participant id — the
// server ranks, the page draws (ADR-0011).
export function standingsByParticipant(
  stage: {standings?: Array<{participantID?: number; metrics?: Record<string, unknown>}>} | undefined,
): Map<number, Record<string, unknown>> {
  const out = new Map<number, Record<string, unknown>>();
  for (const entry of stage?.standings || []) {
    if (entry.participantID) out.set(Number(entry.participantID), entry.metrics || {});
  }
  return out;
}

// A seat as a scheme names it before anyone sits in it — the label a Сетка
// prints. Its key is what pairs a planned бой with its row.
export interface CrossSlot {
  key: string;
  label: string;
}

// CrossBout is one бой of the группа as the page knows it now: who is sitting
// where, what each has scored, and whether the бой is over. A бой nobody has
// touched reports started false and prints nothing in the cell.
export interface CrossBout {
  slots: [CrossSlot | null, CrossSlot | null];
  sides: Array<{name: string; id: number; score: number}>;
  finished: boolean;
  started: boolean;
}

export interface CrossGroup {
  title: string;
  entrants: CrossSlot[];
  bouts: CrossBout[];
  // The block's standings for this группа, by Participant id — whatever the
  // Ranker measured, read for the columns asked for.
  standings: Map<number, Record<string, unknown>>;
}

export interface CrossColumn {
  label: string;
  metric: string;
}

// The columns the КИНСБФ canon prints, and what Троечка's регламент adds in
// front of them: очки, забитые, пропущенные, разница, место.
export const CANON_COLUMNS: CrossColumn[] = [
  {label: "О", metric: "points"},
  {label: "+", metric: "taken"},
  {label: "−", metric: "conceded"},
  {label: "+/−", metric: "diff"},
  {label: "М", metric: "place"},
];

export interface CrosstableSpec {
  groups: CrossGroup[];
  columns?: CrossColumn[];
  className?: string;
  empty?: string;
}

// A рейтинговый балл is очки plus взятые over fifty, so it arrives with the
// binary noise every such sum has — 1.8599999999999999 for 1.86. Ranking uses
// the full value; what is printed is exact in two places.
function round(value: number): number {
  return Math.round(value * 100) / 100;
}

export function buildCrosstables(spec: CrosstableSpec): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = `group-standings ${spec.className || ""}`.trim();
  if (!spec.groups.length) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = spec.empty || "В этой схеме нет групповых таблиц.";
    wrap.appendChild(empty);
    return wrap;
  }
  for (const group of spec.groups) {
    const item = document.createElement("section");
    item.className = "group-standings-item";
    const head = document.createElement("h3");
    head.className = "group-standings-head";
    head.textContent = group.title;
    item.appendChild(head);
    const wrapper = document.createElement("div");
    wrapper.className = "results-wrapper";
    wrapper.appendChild(buildCrosstable(group, spec.columns || CANON_COLUMNS));
    item.appendChild(wrapper);
    wrap.appendChild(item);
  }
  return wrap;
}

function buildCrosstable(group: CrossGroup, columns: CrossColumn[]): HTMLElement {
  const rows = group.entrants.map((slot) => ({key: slot.key, name: slot.label || "", id: 0}));
  const indexByKey = new Map<string, number>();
  rows.forEach((row, i) => indexByKey.set(row.key, i));
  const cellText: string[][] = rows.map(() => rows.map(() => ""));
  const live: boolean[][] = rows.map(() => rows.map(() => false));

  for (const bout of group.bouts) {
    const a = indexByKey.get(bout.slots[0]?.key || "");
    const b = indexByKey.get(bout.slots[1]?.key || "");
    if (a === undefined || b === undefined) continue;
    [a, b].forEach((index, side) => {
      const seat = bout.sides[side];
      if (seat?.name) rows[index].name = seat.name;
      if (seat?.id) rows[index].id = seat.id;
    });
    if (!bout.finished && !bout.started) continue;
    const sa = bout.sides[0]?.score ?? 0;
    const sb = bout.sides[1]?.score ?? 0;
    cellText[a][b] = `${sa} : ${sb}`;
    cellText[b][a] = `${sb} : ${sa}`;
    live[a][b] = live[b][a] = !bout.finished;
  }

  const stat = (row: {id: number}, metric: string): string => {
    const value = group.standings.get(row.id)?.[metric];
    return typeof value === "number" ? formatDisplayText(round(value)) : "";
  };

  const cross = (i: number, j: number) => {
    const cell = td(i === j ? "×" : cellText[i][j]);
    if (i === j) cell.classList.add("cross-diag");
    else cell.classList.toggle("cross-live", live[i][j]);
    return cell;
  };
  return standingsTable({
    className: "group-standings-table crosstable",
    columns: [
      {label: "№", kind: "place"},
      {label: "Команда", kind: "name"},
      ...rows.map((_, i) => ({label: i + 1, kind: "num" as const})),
      ...columns.map((column) => ({label: column.label, kind: "num" as const})),
    ],
    rows: rows.map((row, i) => [
      i + 1,
      row.name,
      ...rows.map((_, j) => cross(i, j)),
      ...columns.map((column) => stat(row, column.metric)),
    ]),
  });
}
