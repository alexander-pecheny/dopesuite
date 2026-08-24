// Тройка's Статистика: the per-player fold across every бой of a game, and its
// table. The sibling of ek-stats, brain-stats and group-stats.
//
// The three players hear each other, because they answer the same вопрос one
// after another. So a correct answer is one of two quite different things: the
// first on that вопрос — the player knew it — or a repeat of one already on
// the table, which is the smaller skill of recognising that the answer just
// given was right. Both pay the same points and neither is the other, so they
// are counted apart and the table sorts on the first.
//
// «Удачные повторы» is the same skill as a rate: of the turns where a correct
// answer was already there to repeat, how often the player took it. A turn
// where nothing correct had been said yet is not a choice about repeating and
// is not counted either way.

import {standingsTable} from "./standings.js";
import * as troika from "./troika-protocol.js";
import type {TroikaState} from "./troika-protocol.js";

export interface TroikaBout {
  state: TroikaState;
  // The бой's two sides as the page knows them: the Participant's name and the
  // names of the players by id.
  sides: Array<{team: string; players: Map<number, string>}>;
}

export interface TroikaPlayerStatsRow {
  player: string;
  team: string;
  bouts: number;
  questions: number;
  first: number;
  repeat: number;
  repeatChances: number;
  repeatRate: number;
  points: number;
}

// computeTroikaPlayerStats folds every бой into one row per (team, player) —
// keyed by both, so namesakes on different teams stay apart.
export function computeTroikaPlayerStats(bouts: ReadonlyArray<TroikaBout>): TroikaPlayerStatsRow[] {
  const rows = new Map<string, TroikaPlayerStatsRow>();
  const seen = new Map<string, Set<number>>();

  bouts.forEach((bout, boutIndex) => {
    const state = bout.state;
    bout.sides.forEach((side, s) => {
      for (let t = 0; t < state.values.length; t++) {
        const value = troika.themeValue(state, t);
        for (let q = 0; q < troika.THEME_QUESTIONS; q++) {
          // Walk the chairs in the order the ведущий asked them, so "already
          // on the table" is a fact about what this player had heard.
          let correctSoFar = 0;
          for (let c = 0; c < troika.CHAIRS; c++) {
            const mark = troika.markAt(state, s, t, q, c);
            if (mark === "") continue;
            const id = troika.chairAt(state, s, t, c);
            const name = side.players.get(id) || "";
            if (!name) continue;
            const key = `${side.team}${name}`;
            let row = rows.get(key);
            if (!row) {
              row = {
                player: name, team: side.team, bouts: 0, questions: 0,
                first: 0, repeat: 0, repeatChances: 0, repeatRate: 0, points: 0,
              };
              rows.set(key, row);
              seen.set(key, new Set());
            }
            const bag = seen.get(key)!;
            if (!bag.has(boutIndex)) {
              bag.add(boutIndex);
              row.bouts++;
            }
            row.questions++;
            if (correctSoFar > 0) row.repeatChances++;
            if (mark === "right") {
              row.points += value;
              if (correctSoFar > 0) row.repeat++;
              else row.first++;
            }
            if (mark === "right") correctSoFar++;
          }
        }
      }
    });
  });

  const out = [...rows.values()];
  for (const row of out) {
    row.repeatRate = row.repeatChances > 0 ? row.repeat / row.repeatChances : 0;
  }
  out.sort((a, b) => b.first - a.first || b.repeat - a.repeat || a.player.localeCompare(b.player, "ru"));
  return out;
}

export function buildTroikaStatsTable(rows: ReadonlyArray<TroikaPlayerStatsRow>): HTMLElement {
  return standingsTable({
    className: "troika-stats",
    columns: [
      {label: "Игрок", kind: "name"},
      {label: "Команда", kind: "name"},
      {label: "Бои", kind: "num"},
      {label: "Взял первым", kind: "num"},
      {label: "Повторил", kind: "num"},
      {label: "Удачные повторы", kind: "num"},
      {label: "Очки", kind: "num"},
    ],
    rows: rows.map((row) => [
      row.player,
      row.team,
      String(row.bouts),
      String(row.first),
      String(row.repeat),
      row.repeatChances > 0 ? `${Math.round(row.repeatRate * 100)}%` : "—",
      String(row.points),
    ]),
  });
}
