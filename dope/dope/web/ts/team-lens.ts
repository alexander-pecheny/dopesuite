// team-lens.ts — how a flat sheet (OD, KSI, Multi) looks at its teams:
// which Division the viewer chose (ADR-0020), which rows that takes, what
// badges a name carries, the chip row that picks a Division, and the order the
// detailed sheet lists the teams in. Each page used to keep this state by hand,
// and the copies drifted; now each page makes one lens and reads from it.
//
// The lens holds no document. It asks the page for the teams each time, so a
// roster change is seen the next time the page calls adopt().

import {ALL_DIVISIONS, divisionChipRow, divisionFromURL, divisionsOf, inDivision, setDivisionInURL} from "./divisions.js";
import {onNavigate} from "./url-state.js";

// LensTeam is what the lens needs of one team row: its Flags and its Number.
// A Number of 0 or below means the team has none (a guest team in Multi,
// a legacy entry).
export interface LensTeam {
  flags?: readonly string[];
  number: number;
}

// TeamSort is the detailed sheet's row order. "name" is the order the server
// lists the teams in, which is by name with the guest teams last. The browser
// does not sort names itself: its collation differs from the server's, and
// switching to it mid-event moved names around (77e17769). "number" is by
// Number, and the teams without one follow in the server's order.
export type TeamSort = "name" | "number";

export interface TeamLensOptions {
  // teams is the document's team rows, or null while there is no document
  // yet. A game with no team rows (a KSI in player mode) answers [].
  teams: () => readonly LensTeam[] | null;
  // hidden is the Divisions this game does not offer (games.hidden_divisions).
  hidden: () => readonly string[] | undefined;
  // moved is told whenever the chosen Division changes, before any render,
  // so the page can drop what it cached for the old one.
  moved?: () => void;
  // render draws the page again after the viewer picks a chip.
  render: () => void;
}

export interface TeamLens {
  // active is the chosen Division, ALL_DIVISIONS for the whole field.
  readonly active: string;
  divisions(): string[];
  // members is the rows the chosen Division takes, undefined for the whole
  // field, so that the whole field is ranked as it was before Divisions.
  members(): number[] | undefined;
  // badges is the Divisions shown after a team's name. There are none inside
  // one Division, because every row would carry the same badge, and a hidden
  // Division is never a badge.
  badges(index: number): string[] | undefined;
  // chips is the row that picks a Division, null when the game offers none.
  chips(): HTMLElement | null;
  // order is the given rows (every row by default) in the detailed sheet's
  // order.
  order(sort: TeamSort, among?: readonly number[]): number[];
  // adopt reads the URL again against the Divisions the document offers now.
  // A stale ?division= is cleared. It reports whether the choice moved.
  adopt(): boolean;
  // follow re-reads the URL whenever the browser moves it. tab applies the
  // page's own tab change and reports whether the tab moved; the page is
  // drawn once if either moved. A tab change never touches the Division.
  follow(tab: () => boolean): void;
}

export function createTeamLens(options: TeamLensOptions): TeamLens {
  let active = ALL_DIVISIONS;

  const flagsOf = (index: number): readonly string[] => options.teams()?.[index]?.flags || [];
  const divisions = (): string[] => divisionsOf((options.teams() || []).map((team) => team.flags), options.hidden() || []);
  const choose = (division: string): void => {
    active = division;
    options.moved?.();
  };

  const adopt = (): boolean => {
    // Before the document arrives the page cannot tell a stale Division from
    // a real one, so it leaves the URL alone.
    if (!options.teams()) return false;
    const next = divisionFromURL(divisions());
    if (next === active) return false;
    choose(next);
    return true;
  };

  return {
    get active() { return active; },
    divisions,
    members() {
      if (active === ALL_DIVISIONS) return undefined;
      const members: number[] = [];
      (options.teams() || []).forEach((team, index) => {
        if (inDivision(team.flags, active)) members.push(index);
      });
      return members;
    },
    badges(index) {
      if (active !== ALL_DIVISIONS) return undefined;
      const hidden = options.hidden() || [];
      return flagsOf(index).filter((flag) => !hidden.includes(flag));
    },
    chips() {
      const offered = divisions();
      if (!offered.length) return null;
      return divisionChipRow(offered, active, (division) => {
        if (division === active) return;
        setDivisionInURL(division);
        choose(division);
        options.render();
      });
    },
    order(sort, among) {
      const teams = options.teams() || [];
      const rows = among ? among.slice() : teams.map((_, index) => index);
      if (sort === "name") return rows.sort((a, b) => a - b);
      const numberOf = (index: number) => {
        const number = teams[index]?.number || 0;
        return number > 0 ? number : Infinity;
      };
      return rows.sort((a, b) => numberOf(a) - numberOf(b) || a - b);
    },
    adopt,
    follow(tab) {
      onNavigate(() => {
        const tabMoved = tab();
        const divisionMoved = adopt();
        if (tabMoved || divisionMoved) options.render();
      });
    },
  };
}
