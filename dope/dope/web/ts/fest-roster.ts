// The roster — the fest-level team→players list, fetched once per fest and drawn
// as the roster table with rating.chgk.info links.

import {nameNode, td} from "./cells.js";
import {resultsTeamCell, standingsTable} from "./standings.js";
import {markNameOverflow} from "./widgets.js";
import S from "./i18nstrings.js";

export interface RosterPlayer {
  name?: string;
  ratingID?: number;
}

export interface RosterTeam {
  name?: string;
  city?: string;
  number?: number;
  ratingID?: number;
  players?: Array<RosterPlayer | string>;
}

// Roster — the fest-level team→players list, shared by every game
// page (EK/OD/KSI, host and viewer). The data is the same for all games in a
// fest, so it is fetched once per festID and cached for the page's lifetime.
const rosterCache = new Map<string | number, Promise<RosterTeam[]>>();

export function fetchFestRoster(festID: string | number | null | undefined): Promise<RosterTeam[]> {
  if (!festID) return Promise.resolve([]);
  const cached = rosterCache.get(festID);
  if (cached) return cached;
  const promise = fetch(`/api/fest/${encodeURIComponent(festID)}/roster`)
    .then((response) => {
      if (!response.ok) throw new Error(`roster ${response.status}`);
      return response.json();
    })
    .then((data: unknown) => {
      const parsed = data as {teams?: unknown} | null;
      return parsed && Array.isArray(parsed.teams) ? (parsed.teams as RosterTeam[]) : [];
    })
    .catch((err: unknown) => {
      // Don't cache a failure — let a later render retry the fetch.
      rosterCache.delete(festID);
      throw err;
    });
  rosterCache.set(festID, promise);
  return promise;
}

// rating.chgk.info deep links: team/player names in the roster view link to
// their rating pages when a rating id is known.
const RATING_TEAM_URL = "https://rating.chgk.info/teams/";
const RATING_PLAYER_URL = "https://rating.chgk.info/players/";

// nonBreakingName joins a player's name parts with U+00A0 so a line never
// breaks inside one person's name. The alternative — white-space: nowrap on the
// chip — cannot break at all, so a name wider than its column pushed the whole
// table sideways rather than wrapping.
function nonBreakingName(name: string | undefined): string {
  return (name || "").replace(/ /g, " ");
}


// buildRosterTable renders the team→players table using the shared results-table
// design-system styling. One row per team: number, name (+ city), player list.
// Team and player names become rating.chgk.info links when a rating id exists.
export function buildRosterTable(teams: RosterTeam[] | null | undefined): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper roster-results-wrapper";
  const list = teams || [];
  if (list.length === 0) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = S.fest.roster.empty();
    wrapper.appendChild(empty);
    return wrapper;
  }

  const hasNumbers = list.some((team) => Number(team.number) > 0);
  const players = (team: RosterTeam) => {
    const cell = td("");
    const members = Array.isArray(team.players) ? team.players : [];
    if (members.length === 0) {
      cell.classList.add("empty");
      cell.textContent = "—";
      return cell;
    }
    for (const player of members) {
      // Tolerate both the current {name, ratingID} shape and a bare string.
      const info: RosterPlayer = typeof player === "string" ? {name: player} : (player || {});
      const chip = document.createElement("span");
      chip.className = "roster-player";
      const href = Number(info.ratingID) > 0 ? `${RATING_PLAYER_URL}${info.ratingID}` : "";
      // Non-breaking spaces inside the name so the column wraps between
      // players, never through one — the cell itself is free to wrap, which
      // is what keeps the roster on screen instead of scrolling sideways.
      chip.appendChild(nameNode(nonBreakingName(info.name), href, "roster-player-name"));
      cell.appendChild(chip);
    }
    return cell;
  };
  wrapper.appendChild(standingsTable({
    className: "roster-results-table",
    columns: [
      ...(hasNumbers ? [{label: "№", kind: "place" as const}] : []),
      {label: S.fest.roster.colTeam(), kind: "name"},
      {label: S.fest.roster.colPlayers(), className: "roster-players"},
    ],
    rows: list.map((team) => [
      ...(hasNumbers ? [Number(team.number) > 0 ? team.number : ""] : []),
      resultsTeamCell(team.name || "", {
        href: Number(team.ratingID) > 0 ? `${RATING_TEAM_URL}${team.ratingID}` : "",
        city: team.city,
      }),
      players(team),
    ]),
  }));
  return wrapper;
}

// buildRosterView returns a container node for the roster tab that fills
// itself asynchronously: it shows a loading line, fetches the fest roster, then
// swaps in the table (or an error line on failure). Safe to drop straight into
// a tab pane by any page — no roster data needs to be threaded through.
export function buildRosterView(festID: string | number | null | undefined): HTMLElement {
  const container = document.createElement("div");
  const loading = document.createElement("p");
  loading.className = "roster-empty";
  loading.textContent = S.fest.roster.loading();
  container.appendChild(loading);

  fetchFestRoster(festID)
    .then((teams) => {
      container.replaceChildren(buildRosterTable(teams));
      // Flag clipped team names so the shared fade + popover kick in, and
      // re-check whenever the container's width changes (tab switch, resize).
      // The popover itself is already handled: the CSS-only variant on OD/KSI,
      // and the page-bound floating popover on the EK host/viewer roots.
      const remeasure = () => markNameOverflow(container, {
        cellSelector: ".results-team",
        nameSelector: ".results-team-name",
        truncatedClass: "results-team-truncated",
      });
      requestAnimationFrame(remeasure);
      if (typeof ResizeObserver === "function") {
        new ResizeObserver(remeasure).observe(container);
      }
    })
    .catch(() => {
      const error = document.createElement("p");
      error.className = "roster-empty";
      error.textContent = S.fest.roster.loadFailed();
      container.replaceChildren(error);
    });
  return container;
}
