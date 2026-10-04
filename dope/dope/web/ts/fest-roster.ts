// The roster — the fest-level team→players list, fetched once per fest and drawn
// as the roster table with rating.chgk.info links.

import {nameNode, td} from "./cells.js";
import {resultsTeamCell, standingsTable} from "./standings.js";
import {icon} from "./icons_gen.js";
import {openGameRosterDialog} from "./game-roster-dialog.js";
import S from "./i18nstrings.js";

export interface RosterPlayer {
  name?: string;
  ratingID?: number;
  // On a game roster: the player's id, and whether they already have
  // something entered in this game, which keeps them on the roster.
  id?: number;
  locked?: boolean;
}

export interface RosterTeam {
  name?: string;
  city?: string;
  number?: number;
  ratingID?: number;
  players?: Array<RosterPlayer | string>;
  // A troika's head team and division, on a Troika game's roster.
  headTeam?: string;
  flags?: string[];
  // On a game roster: the team as the game seats it, and whether the host
  // changed its roster for this game.
  participantID?: number;
  hand?: boolean;
}

export interface RosterTableOptions {
  // The Troika game's roster: the troika column, the team each troika counts
  // for and its division.
  troikas?: boolean;
  // The host may change a team's roster for this game: each row gets a pencil.
  onEdit?: (team: RosterTeam) => void;
}

// FestPlayerChoice is one fest player as the roster dialog suggests them: the
// name to type, and the team they play for to tell namesakes apart.
export interface FestPlayerChoice {
  Name: string;
  Team: string;
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
export function buildRosterTable(teams: RosterTeam[] | null | undefined, options: RosterTableOptions = {}): HTMLElement {
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
    // Under the players rather than under the team's name: the name cell
    // clips and fades on a phone, and this line must be read whole.
    if (team.hand) {
      const note = document.createElement("div");
      note.className = "hint";
      note.textContent = S.fest.roster.handNote();
      cell.appendChild(note);
    }
    return cell;
  };
  const troikas = Boolean(options.troikas);
  const onEdit = options.onEdit;
  wrapper.appendChild(standingsTable({
    // Without a № column (troikas, a fest not numbered yet) the frozen name
    // column must start at the edge: the shared rule offsets it by a № column
    // that is not there, and it slid over the players' names.
    className: hasNumbers ? "roster-results-table" : "roster-results-table roster-unnumbered",
    columns: [
      ...(hasNumbers ? [{label: "№", kind: "place" as const}] : []),
      {label: troikas ? S.fest.roster.colTroika() : S.fest.roster.colTeam(), kind: "name"},
      {label: S.fest.roster.colPlayers(), className: "roster-players"},
      ...(onEdit ? [{label: "", className: "roster-edit"}] : []),
    ],
    rows: list.map((team) => [
      ...(hasNumbers ? [Number(team.number) > 0 ? team.number : ""] : []),
      resultsTeamCell(team.name || "", {
        href: Number(team.ratingID) > 0 ? `${RATING_TEAM_URL}${team.ratingID}` : "",
        // A troika has no city: the line under its name says whom it plays
        // for and in which division, which keeps the table to three columns on a
        // phone.
        city: troikas ? troikaLine(team) : team.city,
      }),
      players(team),
      ...(onEdit ? [td(editButton(team, onEdit))] : []),
    ]),
  }));
  return wrapper;
}

function editButton(team: RosterTeam, onEdit: (team: RosterTeam) => void): HTMLButtonElement {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "action-icon";
  const label = S.fest.rosterEdit.label(team.name || "");
  button.title = label;
  button.setAttribute("aria-label", label);
  button.appendChild(icon("pencil"));
  button.addEventListener("click", () => onEdit(team));
  return button;
}

function troikaLine(team: RosterTeam): string {
  return [team.headTeam ? S.fest.roster.forTeam(team.headTeam) : "", (team.flags || []).join(", ")]
    .filter(Boolean).join(" · ");
}

// buildRosterView returns a container node for the roster tab that fills
// itself asynchronously: it shows a loading line, fetches the fest roster, then
// swaps in the table (or an error line on failure). Safe to drop straight into
// a tab pane by any page — no roster data needs to be threaded through.
export function buildRosterView(festID: string | number | null | undefined): HTMLElement {
  return rosterView(fetchFestRoster(festID), {});
}

export interface GameRosterOptions {
  // The host of a team buzzer format may change each team's roster for this
  // game.
  editable?: boolean;
  // A Troika game, for the host: the troikas page, where a troika's people
  // change.
  troikasHref?: string;
}

// GameRosterData is what a game's roster endpoint sends. entrants marks a
// a Troika's troikas; game marks a team format's teams with the rosters they
// play this game with; neither is the fest roster of a game with no teams yet.
interface GameRosterData {
  teams: RosterTeam[];
  entrants: boolean;
  game: boolean;
  choices: FestPlayerChoice[];
}

// fetchGameRoster reads a game's roster tab, with the fest's players to suggest
// when the host is about to edit it.
export async function fetchGameRoster(apiBase: string, choices = false): Promise<GameRosterData> {
  const response = await fetch(`${apiBase}/roster${choices ? "?choices=1" : ""}`);
  if (!response.ok) throw new Error(`roster ${response.status}`);
  const parsed = (await response.json()) as Partial<Record<keyof GameRosterData, unknown>> | null;
  return {
    teams: Array.isArray(parsed?.teams) ? (parsed!.teams as RosterTeam[]) : [],
    entrants: Boolean(parsed?.entrants),
    game: Boolean(parsed?.game),
    choices: Array.isArray(parsed?.choices) ? (parsed!.choices as FestPlayerChoice[]) : [],
  };
}

// buildGameRosterView is a game page's roster tab: who that game seats and the
// roster each team plays it with, rather than the fest's teams. A Troika shows
// its troikas, with the team each counts for and its division. It is fetched
// afresh each time, since the troikas page and the roster dialog change it
// while the game page is open.
export function buildGameRosterView(apiBase: string, options: GameRosterOptions = {}): HTMLElement {
  const container = document.createElement("div");
  container.className = "u-col u-gap-sm";
  const load = (): void => {
    fillRosterView(container, fetchGameRoster(apiBase, Boolean(options.editable)).then((d): RosterFill => {
      if (d.entrants) {
        const href = options.troikasHref;
        return {teams: d.teams, options: {troikas: true}, after: href ? () => container.appendChild(troikasLink(href)) : undefined};
      }
      // A Game with no teams of its own yet sends the fest's teams, drawn as
      // the fest roster, with nothing to edit.
      if (!d.game || !options.editable) return {teams: d.teams, options: {}};
      return {teams: d.teams, options: {onEdit: (team) => openGameRosterDialog(team, d.choices, apiBase, load)}};
    }));
  };
  load();
  return container;
}

function troikasLink(href: string): HTMLElement {
  const note = document.createElement("p");
  note.className = "hint";
  const link = document.createElement("a");
  link.href = href;
  link.textContent = S.fest.roster.troikasLink();
  note.appendChild(link);
  return note;
}

// RosterFill is what a roster tab draws once its data arrives: the teams, how
// to draw them, and anything that goes under the table.
interface RosterFill {
  teams: RosterTeam[];
  options: RosterTableOptions;
  after?: () => void;
}

function rosterView(teams: Promise<RosterTeam[]>, options: RosterTableOptions): HTMLElement {
  const container = document.createElement("div");
  fillRosterView(container, teams.then((teams) => ({teams, options})));
  return container;
}

// fillRosterView draws a roster tab into its container when the data arrives.
// The loading line shows only the first time: a refill after an edit keeps the
// old table on screen until the new one replaces it.
function fillRosterView(container: HTMLElement, fill: Promise<RosterFill>): void {
  if (!container.firstChild) {
    const loading = document.createElement("p");
    loading.className = "roster-empty";
    loading.textContent = S.fest.roster.loading();
    container.appendChild(loading);
  }

  fill
    .then(({teams, options, after}) => {
      container.replaceChildren(buildRosterTable(teams, options));
      after?.();
    })
    .catch(() => {
      const error = document.createElement("p");
      error.className = "roster-empty";
      error.textContent = S.fest.roster.loadFailed();
      container.replaceChildren(error);
    });
}

