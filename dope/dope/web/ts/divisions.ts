// divisions.ts — a Division as the viewer looks at one (ADR-0020). A Division is
// not a ranking scope: it is the set of teams carrying one Flag, and the page
// re-ranks the document within it. This module knows which Divisions a game
// offers, whether a team stands in the chosen one, where the choice lives in
// the URL, and what the chip row that picks it is made of.
//
// Pure of any one game's document: the OD page hands it its teams' flags, the
// KSI page its participants', and both get the same answers.

import {renderTabBar} from "./widgets.js";
import {param, setParam} from "./url-state.js";
import S from "./i18nstrings.js";

// The query parameter the choice lives in, and the whole field, which the URL
// spells by carrying no parameter at all.
export const DIVISION_PARAM = "division";
export const ALL_DIVISIONS = "";

// divisionsOf is every distinct Flag among a game's teams, in first-seen order
// — the order the chips are offered in. A game whose teams carry no Flag offers
// no Divisions, and the page shows no chips.
export function divisionsOf(flagLists: ReadonlyArray<readonly string[] | undefined>): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const flags of flagLists) {
    for (const flag of flags || []) {
      if (!flag || seen.has(flag)) continue;
      seen.add(flag);
      out.push(flag);
    }
  }
  return out;
}

// inDivision reports whether a team belongs in the chosen Division. The whole
// field takes everyone, and a team with two Flags stands in both.
export function inDivision(flags: readonly string[] | undefined, division: string): boolean {
  if (division === ALL_DIVISIONS) return true;
  return (flags || []).includes(division);
}

// divisionFromURL is the Division the address bar names, or the whole field
// when it names none this game offers — a stale or mistyped value is cleared as
// it is read, so the URL never claims a Division the table is not showing.
export function divisionFromURL(divisions: readonly string[]): string {
  const value = param(DIVISION_PARAM);
  if (!value) return ALL_DIVISIONS;
  if (divisions.includes(value)) return value;
  setParam(DIVISION_PARAM, "");
  return ALL_DIVISIONS;
}

export function setDivisionInURL(division: string): void {
  setParam(DIVISION_PARAM, division);
}

// divisionChipRow is the single-choice row: the whole field first, then one
// chip per Division. It is the tab strip's own primitive — a chip per Division
// is the same gesture a tab per pane is — so the two cannot drift apart.
export function divisionChipRow(
  divisions: readonly string[],
  active: string,
  onPick: (division: string) => void,
): HTMLElement {
  const row = document.createElement("div");
  row.className = "match-tabs division-chips";
  row.setAttribute("role", "tablist");
  row.setAttribute("aria-label", S.screen.division.label());
  renderTabBar(
    row,
    [{key: ALL_DIVISIONS, label: S.screen.division.all()}, ...divisions.map((division) => ({key: division, label: division}))],
    active,
    onPick,
  );
  return row;
}
