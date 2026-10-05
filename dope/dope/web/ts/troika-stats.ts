// Troika's statistics: the per-player fold across every bout of a game, and its
// table. The sibling of ek-stats, brain-stats and group-stats.
//
// The three players hear each other, because they answer the same question one
// after another. So a correct answer is one of two quite different things: the
// first on that question — the player knew it — or a repeat of one already on
// the table, which is the smaller skill of recognising that the answer just
// given was right. Both pay the same points and neither is the other, so they
// are counted apart and the table sorts on the first.
//
// The repeat rate is the same skill as a rate: of the turns where a correct
// answer was already there to repeat, how often the player took it. A turn
// where nothing correct had been said yet is not a choice about repeating and
// is not counted either way.

import {percentText} from "./cells.js";
import {standingsTable} from "./standings.js";
import * as troika from "./troika-protocol.js";
import S from "./i18nstrings.js";
import type {TroikaState} from "./troika-protocol.js";

export interface TroikaBout {
  state: TroikaState;
  // The bout's two sides as the page knows them: the Participant's name and the
  // names of the players by id.
  sides: Array<{team: string; players: Map<number, string>}>;
}

export interface TroikaPlayerStatsRow {
  player: string;
  team: string;
  bouts: number;
  // chairs counts the themes played in each chair: the two side chairs, then the anchor.
  chairs: [number, number, number];
  questions: number;
  correct: number;
  correctRate: number;
  first: number;
  repeat: number;
  repeatChances: number;
  repeatRate: number;
  points: number;
}

// computeTroikaPlayerStats folds every bout into one row per (team, player) —
// keyed by both, so namesakes on different teams stay apart. A theme counts
// for a chair once the side has a mark in it: who sat where in a theme nobody
// played says nothing.
export function computeTroikaPlayerStats(bouts: ReadonlyArray<TroikaBout>): TroikaPlayerStatsRow[] {
  const rows = new Map<string, TroikaPlayerStatsRow>();
  const seen = new Map<string, Set<number>>();
  const rowOf = (team: string, name: string, boutIndex: number): TroikaPlayerStatsRow => {
    const key = `${team}\u0000${name}`;
    let row = rows.get(key);
    if (!row) {
      row = {
        player: name, team, bouts: 0, chairs: [0, 0, 0], questions: 0, correct: 0, correctRate: 0,
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
    return row;
  };

  bouts.forEach((bout, boutIndex) => {
    const state = bout.state;
    bout.sides.forEach((side, s) => {
      for (let t = 0; t < state.values.length; t++) {
        const value = troika.themeValue(state, t);
        let played = false;
        for (let q = 0; q < troika.THEME_QUESTIONS && !played; q++) {
          for (let c = 0; c < troika.CHAIRS; c++) if (troika.markAt(state, s, t, q, c) !== "") played = true;
        }
        if (!played) continue;
        for (let c = 0; c < troika.CHAIRS; c++) {
          const name = side.players.get(troika.chairAt(state, s, t, c)) || "";
          if (name) rowOf(side.team, name, boutIndex).chairs[c]++;
        }
        for (let q = 0; q < troika.THEME_QUESTIONS; q++) {
          // Walk the chairs in the order the host asked them, so "already
          // on the table" is a fact about what this player had heard.
          let correctSoFar = 0;
          for (let c = 0; c < troika.CHAIRS; c++) {
            const mark = troika.markAt(state, s, t, q, c);
            if (mark === "") continue;
            const name = side.players.get(troika.chairAt(state, s, t, c)) || "";
            // An unseated chair still put its answer on the table, so it is
            // what the next player heard, even though no row can be credited.
            if (!name) {
              if (mark === "right") correctSoFar++;
              continue;
            }
            const row = rowOf(side.team, name, boutIndex);
            row.questions++;
            if (correctSoFar > 0) row.repeatChances++;
            if (mark === "right") {
              row.correct++;
              row.points += value;
              if (correctSoFar > 0) row.repeat++;
              else row.first++;
              correctSoFar++;
            }
          }
        }
      }
    });
  });

  const out = [...rows.values()];
  for (const row of out) {
    row.correctRate = row.questions > 0 ? row.correct / row.questions : 0;
    row.repeatRate = row.repeatChances > 0 ? row.repeat / row.repeatChances : 0;
  }
  // By points: every right answer pays, the first and the repeat alike. The
  // anchor answers last and repeats most, so first answers alone would rank
  // the chairs rather than the players.
  out.sort((a, b) => b.points - a.points || b.correct - a.correct || b.first - a.first || a.player.localeCompare(b.player, "ru"));
  return out;
}

// buildTroikaStatsTable wears the per-player stats skin EK, Hamsa and solo SI
// share: flush left, the two name columns as wide as their names up to a cap,
// and a longer name fading out with its popover.
export function buildTroikaStatsTable(rows: ReadonlyArray<TroikaPlayerStatsRow>): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper ek-stats-wrapper";
  if (rows.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = S.troika.stats.empty();
    wrapper.appendChild(empty);
    return wrapper;
  }
  wrapper.appendChild(standingsTable({
    className: "ek-stats-table",
    sortKey: "troika-stats",
    columns: [
      {label: S.troika.stats.player(), kind: "name", className: "ek-stats-name ek-stats-player"},
      {label: S.troika.stats.team(), kind: "name", className: "ek-stats-name"},
      {label: S.troika.stats.bouts(), kind: "num"},
      {label: S.troika.stats.chairs(), kind: "num"},
      {label: S.troika.stats.questions(), kind: "num"},
      {label: S.troika.stats.correct(), kind: "num"},
      {label: S.troika.stats.correctRate(), kind: "num"},
      {label: S.troika.stats.first(), kind: "num"},
      {label: S.troika.stats.repeat(), kind: "num"},
      {label: S.troika.stats.repeatRate(), kind: "num"},
      {label: S.troika.stats.points(), kind: "num", className: "ek-stats-sum"},
    ],
    rows: rows.map((row) => [
      row.player,
      row.team,
      String(row.bouts),
      row.chairs.join(" / "),
      String(row.questions),
      String(row.correct),
      row.questions > 0 ? percentText(row.correctRate) : "—",
      String(row.first),
      String(row.repeat),
      row.repeatChances > 0 ? percentText(row.repeatRate) : "—",
      String(row.points),
    ]),
  }));
  return wrapper;
}
