// The roster editor: rows of a suggest over buff's player mirror, a
// not-in-the-base fallback that turns a row into three typed name fields with id
// 0, and one captain. Flags are derived server-side on save, so the editor
// never offers them. The whole roster travels in one hidden field, which is
// what the application form posts.

export type RosterPlayer = {
  player_id: number;
  surname: string;
  name: string;
  patronymic: string;
  captain: boolean;
};

// A suggested player carries how many games the mirror knows them by, which is
// how a Representative tells namesakes apart.
export type SuggestedPlayer = RosterPlayer & {games?: number};

// playerChoice is one suggested player as the shared dropdown draws it: the
// name and id on the line, the games it is known by as the hint.
export function playerChoice(p: SuggestedPlayer): Choice {
  const games = Number(p.games) || 0;
  const hint = games > 0 ? `${S.venues.rosterEditor.gamesCount(games)} · ${p.player_id}` : String(p.player_id);
  return {value: JSON.stringify(p), label: fullName(p), hint};
}

import S from "./i18nstrings.js";
import {autocomplete} from "../../../../dopeuikit/assets/ts/suggest.js";
import type {Choice} from "../../../../dopeuikit/assets/ts/suggest.js";

export const MAX_ROSTER = 6;

export function emptyPlayer(): RosterPlayer {
  return { player_id: 0, surname: "", name: "", patronymic: "", captain: false };
}

export function parseRoster(raw: string): RosterPlayer[] {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw || "[]");
  } catch {
    return [];
  }
  if (!Array.isArray(parsed)) return [];
  return parsed.map((entry) => {
    const p = (entry ?? {}) as Partial<RosterPlayer>;
    return {
      player_id: Number(p.player_id) || 0,
      surname: String(p.surname ?? "").trim(),
      name: String(p.name ?? "").trim(),
      patronymic: String(p.patronymic ?? "").trim(),
      captain: Boolean(p.captain),
    };
  });
}

export function serializeRoster(players: RosterPlayer[]): string {
  return JSON.stringify(players.filter((p) => fullName(p) !== ""));
}

export function fullName(p: RosterPlayer): string {
  return [p.surname, p.name, p.patronymic].map((s) => s.trim()).filter(Boolean).join(" ");
}

// suggestLabel is «Surname Name Patronymic (id)», the line the suggest shows,
// with the games the mirror knows the player by when it does.
export function suggestLabel(p: SuggestedPlayer): string {
  const name = fullName(p);
  if (p.player_id <= 0) return name;
  const games = Number(p.games) || 0;
  return games > 0
    ? `${name} (${p.player_id}) · ${S.venues.rosterEditor.gamesCount(games)}`
    : `${name} (${p.player_id})`;
}

// setCaptain keeps exactly one captain: picking a new one clears the old.
export function setCaptain(players: RosterPlayer[], index: number): RosterPlayer[] {
  return players.map((p, i) => ({ ...p, captain: i === index }));
}

// rosterWarning is what the form says about the roster's size; "" when it is
// nothing to remark on.
export function rosterWarning(players: RosterPlayer[]): string {
  const filled = players.filter((p) => fullName(p) !== "");
  if (filled.length > MAX_ROSTER) return S.venues.rosterEditor.tooMany(String(MAX_ROSTER));
  if (filled.length > 0 && !filled.some((p) => p.captain)) return S.venues.rosterEditor.noCaptain();
  return "";
}

function el(tag: string, className?: string): HTMLElement {
  const node = document.createElement(tag);
  if (className) node.className = className;
  return node;
}

function input(className: string, value: string, placeholder: string): HTMLInputElement {
  const node = document.createElement("input");
  node.className = className;
  node.type = "text";
  node.value = value;
  node.placeholder = placeholder;
  node.autocomplete = "off";
  return node;
}

async function fetchJSON<T>(url: string): Promise<T | null> {
  try {
    const response = await fetch(url, {headers: {Accept: "application/json"}});
    if (!response.ok) return null;
    return (await response.json()) as T;
  } catch {
    return null;
  }
}

// buff names a player's id `id`; a роль in a состав names it `player_id`, and a
// row that loses it stops being a mirror player and turns into three typed
// fields. Every buff answer goes through here.
type MirrorPlayer = {id: number; surname: string; name: string; patronymic: string; games?: number};

function asRosterPlayers(rows: MirrorPlayer[] | null): SuggestedPlayer[] {
  if (!Array.isArray(rows)) return [];
  return rows.map((r) => ({
    player_id: r.id,
    surname: r.surname ?? "",
    name: r.name ?? "",
    patronymic: r.patronymic ?? "",
    captain: false,
    games: Number(r.games) || 0,
  }));
}

async function fetchPlayers(query: string): Promise<SuggestedPlayer[]> {
  return asRosterPlayers(await fetchJSON<MirrorPlayer[]>(`/api/buff/players?q=${encodeURIComponent(query)}`));
}

// mountRosterEditor draws the editor over one [data-roster-editor] container.
export function mountRosterEditor(container: HTMLElement): void {
  const field = container.querySelector<HTMLInputElement>("[data-roster-json]");
  if (!field) return;
  let players = parseRoster(field.value);
  // There is always a row waiting for the next player: the add button is
  // for putting one back after a removal, not for every name.
  if (players.length === 0 || fullName(players[players.length - 1]) !== "") players.push(emptyPlayer());
  const rows = el("div", "u-col u-gap-sm");
  const warning = el("p", "hint");
  const button = (label: string): HTMLButtonElement => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "btn btn-ghost";
    b.textContent = label;
    return b;
  };
  const add = button(S.venues.rosterEditor.addPlayer());
  // Two ways not to type six names: what rating.chgk.info has the team down as,
  // and whatever this person named last time. Most составы are one of the two.
  const fillBase = button(S.venues.rosterEditor.fillBase());
  const fillPrevious = button(S.venues.rosterEditor.fillPrevious());
  const fillRow = el("div", "u-row u-gap-sm u-wrap u-align-center");
  fillRow.append(add, fillBase, fillPrevious);
  container.append(rows, warning, fillRow);

  const sync = (): void => {
    field.value = serializeRoster(players);
    warning.textContent = rosterWarning(players);
  };

  const draw = (): void => {
    rows.textContent = "";
    players.forEach((player, index) => {
      rows.append(drawRow(player, index));
    });
    sync();
  };

  const drawRow = (player: RosterPlayer, index: number): HTMLElement => {
    const row = el("div", "u-row u-gap-sm u-wrap u-align-center");
    // A row is either a mirror player or a hand-typed one; an empty row is a
    // suggest until the fallback turns it into the three name fields. The name
    // takes the width the row has left, so a full three-part name fits.
    const name = player.player_id > 0 || fullName(player) === ""
      ? drawSuggest(player, index)
      : drawTyped(player, index);
    name.classList.add("u-grow");
    row.append(name, drawCaptain(player, index), drawRemove(index));
    return row;
  };

  const focusRow = (index: number): void => {
    (rows.children[index] as HTMLElement | undefined)?.querySelector("input")?.focus();
  };

  const appendEmptyRow = (focus: boolean): void => {
    players.push(emptyPlayer());
    rows.append(drawRow(players[players.length - 1], players.length - 1));
    sync();
    if (focus) focusRow(players.length - 1);
  };

  // After a pick the submitter's next act is the next player, so the row for
  // them is already there and waiting rather than behind the add button.
  const openNextRow = (): void => {
    if (fullName(players[players.length - 1]) !== "") {
      appendEmptyRow(true);
      return;
    }
    focusRow(players.length - 1);
  };

  // A filled состав replaces whatever was there: the buttons are a starting
  // point, and half a roster from two sources is nobody's team.
  const fill = (found: RosterPlayer[]): void => {
    if (found.length === 0) return;
    players = found.map((p) => ({...p}));
    if (!players.some((p) => p.captain)) players[0].captain = true;
    players.push(emptyPlayer());
    draw();
  };

  const say = (message: string): void => {
    warning.textContent = message;
  };

  fillBase.addEventListener("click", () => {
    const id = Number(container.closest("form")?.querySelector<HTMLInputElement>("[data-team-id]")?.value || "");
    const on = container.getAttribute("data-roster-editor") || "";
    if (!id) {
      say(S.venues.rosterEditor.fillBaseNone());
      return;
    }
    void fetchJSON<MirrorPlayer[]>(`/api/buff/team/${id}/roster?on=${encodeURIComponent(on)}`).then((rows) => {
      const found = asRosterPlayers(rows);
      if (found.length === 0) {
        say(S.venues.rosterEditor.fillBaseNone());
        return;
      }
      fill(found);
    });
  });

  fillPrevious.addEventListener("click", () => {
    void fetchJSON<RecentRoster[]>("/api/venues/my-rosters").then((found) => {
      if (!found || found.length === 0) {
        say(S.venues.rosterEditor.fillPreviousNone());
        return;
      }
      pickPrevious(found, fillPrevious, fill);
    });
  });

  const drawSuggest = (player: RosterPlayer, index: number): HTMLElement => {
    const box = el("div", "u-col u-gap-sm");
    const query = input("input", suggestLabel(player), S.venues.rosterEditor.playerPlaceholder());
    // Wide enough that the row wraps its controls under it on a phone rather
    // than squeezing the name into a third of the line.
    query.size = 28;
    autocomplete(query, async (typed) => {
      const found = await fetchPlayers(typed);
      const choices = found.map(playerChoice);
      // The mirror does not know a player who has never played; the row says
      // so and turns into three typed fields.
      if (typed.trim() !== "") choices.push({value: MANUAL, label: S.venues.rosterEditor.notInBase()});
      return choices;
    }, (choice, typed) => {
      const manual = choice.value === MANUAL;
      players[index] = manual
        ? {...emptyPlayer(), surname: typed.trim(), captain: players[index].captain}
        : {...(JSON.parse(choice.value) as SuggestedPlayer), captain: players[index].captain};
      draw();
      // A hand-typed player still has to be named; the next row waits for that.
      if (manual) focusRow(index);
      else openNextRow();
    });
    box.append(query);
    return box;
  };

  const drawTyped = (player: RosterPlayer, index: number): HTMLElement => {
    const box = el("div", "u-row u-gap-sm u-wrap");
    const fields: Array<[keyof RosterPlayer, string]> = [
      ["surname", S.venues.rosterEditor.surname()],
      ["name", S.venues.rosterEditor.name()],
      ["patronymic", S.venues.rosterEditor.patronymic()],
    ];
    for (const [key, placeholder] of fields) {
      const node = input("input u-grow", String(player[key] ?? ""), placeholder);
      node.addEventListener("input", () => {
        players[index] = { ...players[index], [key]: node.value };
        sync();
        // Named at last: open the next row, without taking the caret out of
        // the one being typed into.
        if (index === players.length - 1 && fullName(players[index]) !== "") appendEmptyRow(false);
      });
      box.append(node);
    }
    return box;
  };

  const drawCaptain = (player: RosterPlayer, index: number): HTMLElement => {
    const label = el("label", "u-row u-gap-sm u-align-center");
    const radio = document.createElement("input");
    radio.type = "radio";
    radio.name = `${field.name}-captain`;
    radio.checked = player.captain;
    radio.addEventListener("change", () => {
      players = setCaptain(players, index);
      draw();
    });
    const caption = document.createElement("span");
    caption.textContent = S.venues.rosterEditor.captain();
    label.append(radio, caption);
    return label;
  };

  const drawRemove = (index: number): HTMLElement => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "btn btn-ghost";
    button.textContent = S.venues.rosterEditor.remove();
    button.addEventListener("click", () => {
      players.splice(index, 1);
      if (players.length === 0) players.push(emptyPlayer());
      draw();
    });
    return button;
  };

  add.addEventListener("click", () => {
    players.push(emptyPlayer());
    draw();
  });
  draw();
}

type BuffTeam = {id: number; name: string; town: string};
type BuffTournament = {id: number; name: string; type: string};

const MANUAL = "\u0000manual";

// mountTeamField is the one team box. Which team an application is for is a
// choice before it — a team rating.chgk.info already knows, or a new one — and
// the box obeys that choice: a suggest over buff by name or by id, whose pick
// fills the hidden rating id, or a plain name with no id at all.
export function mountTeamField(field: HTMLInputElement): void {
  const form = field.form;
  const id = form?.querySelector<HTMLInputElement>("[data-team-id]") ?? null;
  const hint = document.createElement("p");
  hint.className = "hint";
  field.after(hint);
  const seen = new Map<Choice, BuffTeam>();
  const existing = (): boolean =>
    form?.querySelector<HTMLInputElement>('input[name="team_kind"]:checked')?.value !== "new";

  // A team nobody picked from the list has no id, and under «Существующая
  // команда» that is the one thing the form cannot save. It is said once the
  // person has stopped typing, never while they are: the suggest draws in this
  // exact spot, and a nag sitting under a live list reads as «no results».
  const say = (): void => {
    const wanted = existing() && !Number(id?.value) && field.value.trim() !== "";
    hint.textContent = wanted && document.activeElement !== field ? S.venues.reg.teamPickRequired() : "";
  };

  autocomplete(field, async (text) => {
    if (!existing() || text.trim() === "") return [];
    const teams = (await fetchJSON<BuffTeam[]>(`/api/buff/teams?q=${encodeURIComponent(text)}`)) || [];
    return teams.map((team) => {
      const choice: Choice = {value: team.name, label: team.name, hint: `${team.id}${team.town ? ` · ${team.town}` : ""}`};
      seen.set(choice, team);
      return choice;
    });
  }, (choice) => {
    const team = seen.get(choice);
    if (id && team) id.value = String(team.id);
    say();
  });

  // Typing past a pick unpicks it: the box no longer says what the id says.
  field.addEventListener("input", () => {
    const team = [...seen.values()].find((t) => t.name === field.value);
    if (id && !team) id.value = "";
    say();
  });
  field.addEventListener("focus", say);
  field.addEventListener("blur", () => setTimeout(say, 200));
  form?.addEventListener("change", (event) => {
    const el = event.target;
    if (!(el instanceof HTMLInputElement) || el.name !== "team_kind") return;
    if (id && !existing()) id.value = "";
    say();
  });
  say();
}

// mountBuffTournamentField is the one tournament field: what is typed searches
// buff by name or by id, and what is kept is the id, so the field names the
// tournament it holds underneath itself.
export function mountBuffTournamentField(field: HTMLInputElement): void {
  const chosen = document.createElement("p");
  chosen.className = "hint";
  field.after(chosen);
  const on = field.getAttribute("data-buff-tournament") || "";
  const seen = new Map<number, BuffTournament>();

  const find = async (text: string): Promise<BuffTournament[]> => {
    const rows = await fetchJSON<BuffTournament[]>(
      `/api/buff/tournaments?q=${encodeURIComponent(text)}&on=${encodeURIComponent(on)}`,
    );
    for (const t of rows || []) seen.set(t.id, t);
    return rows || [];
  };
  const name = (t: BuffTournament | undefined): void => {
    chosen.textContent = t ? `${t.name} · ${t.type}` : "";
  };

  autocomplete(field, async (text) => {
    const rows = await find(text);
    return rows.map((t) => ({value: String(t.id), label: t.name, hint: `${t.type} · ${t.id}`}));
  }, (choice) => name(seen.get(Number(choice.value))));

  const id = Number(field.value.trim());
  if (id) void find(String(id)).then(() => name(seen.get(id)));
}

export interface RecentRoster {
  teamName: string;
  at: string;
  roster: RosterPlayer[];
}

// pickPrevious offers the составы this person has named before, newest first,
// as the same dropdown every other suggest in the app uses.
function pickPrevious(
  found: RecentRoster[],
  anchor: HTMLElement,
  take: (players: RosterPlayer[]) => void,
): void {
  const pop = el("div", "menu-dropdown suggest-pop");
  const dismiss = (): void => pop.remove();
  found.forEach((r) => {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "menu-item";
    const label = el("span");
    label.textContent = r.teamName || S.venues.rosterEditor.fillPreviousTitle();
    const hint = el("span", "suggest-hint");
    hint.textContent = r.roster.map(fullName).filter(Boolean).join(", ");
    item.append(label, hint);
    item.addEventListener("mousedown", (event) => {
      event.preventDefault();
      dismiss();
      take(r.roster);
    });
    pop.append(item);
  });
  const host = anchor.parentElement;
  if (!host) return;
  host.classList.add("suggest-anchor");
  host.append(pop);
  const hostBox = host.getBoundingClientRect();
  const box = anchor.getBoundingClientRect();
  pop.style.top = `${Math.round(box.bottom - hostBox.top)}px`;
  pop.style.left = `${Math.round(box.left - hostBox.left)}px`;
  pop.style.minWidth = `${Math.round(box.width)}px`;
  setTimeout(() => document.addEventListener("click", dismiss, {once: true}), 0);
}

export function mountRosterEditors(doc: Document): void {
  doc.querySelectorAll<HTMLElement>("[data-roster-editor]").forEach(mountRosterEditor);
  doc.querySelectorAll<HTMLInputElement>("input[data-team-field]").forEach(mountTeamField);
  doc.querySelectorAll<HTMLInputElement>("input[data-buff-tournament]").forEach(mountBuffTournamentField);
  doc.querySelectorAll<HTMLInputElement>("input[data-rating-venue]").forEach(mountRatingVenueField);
}

if (typeof document !== "undefined") {
  mountRosterEditors(document);
}

// A Venue is a venue rating.chgk.info already has: the field is a suggest
// over their catalogue, by id, by name or by the town it is in.
const CYRILLIC: Record<string, string> = {
  а: "a", б: "b", в: "v", г: "g", д: "d", е: "e", ё: "e", ж: "zh", з: "z", и: "i",
  й: "y", к: "k", л: "l", м: "m", н: "n", о: "o", п: "p", р: "r", с: "s", т: "t",
  у: "u", ф: "f", х: "h", ц: "c", ч: "ch", ш: "sh", щ: "sch", ъ: "", ы: "y", ь: "",
  э: "e", ю: "yu", я: "ya",
};

// slugify is the URL a venue gets when its Representative did not name one.
export function slugify(name: string): string {
  const latin = [...name.toLowerCase()].map((ch) => CYRILLIC[ch] ?? ch).join("");
  const slug = latin.replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 64);
  return /[a-z]/.test(slug) ? slug : "";
}

type RatingVenue = {id: number; name: string; town: string};

async function fetchVenues(query: string): Promise<RatingVenue[]> {
  return (await fetchJSON<RatingVenue[]>(`/api/rating/venues?q=${encodeURIComponent(query)}`)) || [];
}

export function mountRatingVenueField(field: HTMLInputElement): void {
  const form = field.form;
  const named = (name: string): HTMLInputElement | null =>
    form?.querySelector<HTMLInputElement>(`[data-${name}]`) ?? null;
  const chosen = document.createElement("p");
  chosen.className = "hint";
  field.after(chosen);
  const seen = new Map<number, RatingVenue>();

  const took = (venue: RatingVenue): void => {
    chosen.textContent = `${venue.name} · ${venue.town}`;
    // The edit form still names the Venue itself; the create form does not,
    // and takes the name rating.chgk.info has for it.
    const name = named("venue-name");
    const city = named("venue-city");
    if (name) name.value = venue.name;
    if (city) city.value = venue.town;
    // A slug the Representative typed is theirs; an empty one is derived.
    const slug = form?.querySelector<HTMLInputElement>('input[name="slug"]');
    if (slug && !slug.value.trim()) slug.value = slugify(venue.name);
  };

  const offer = async (typed: string): Promise<Choice[]> => {
    const found = await fetchVenues(typed);
    for (const venue of found) seen.set(venue.id, venue);
    return found.map((v) => ({value: String(v.id), label: v.name, hint: `${v.town} · ${v.id}`}));
  };

  autocomplete(field, offer, (choice) => {
    const venue = seen.get(Number(choice.value));
    if (venue) took(venue);
  });

  // A Venue already made says which venue it is, not just its number.
  const id = Number(field.value.trim());
  if (id) {
    void fetchVenues(field.value.trim()).then((found) => {
      const venue = found.find((v) => v.id === id);
      if (venue) chosen.textContent = `${venue.name} · ${venue.town}`;
    });
  }
}
