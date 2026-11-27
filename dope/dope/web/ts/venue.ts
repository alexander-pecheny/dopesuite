// Venues (ADR-0037): a fest's room, and a game's numbered venue, which its
// map puts in a room with its hosts. The labels a bout's head reads, the two
// tables, and the bout's dialog.

import {option, td} from "./cells.js";
import {nameCell} from "./name-cell.js";
import {standingsTable} from "./standings.js";
import type {StandingsColumn} from "./standings.js";
import {icon, iconed} from "./icons_gen.js";
import S from "./i18nstrings.js";

// Visible width, in characters, of the new room's title field.
const TITLE_FIELD_SIZE = 24;

export type VenueLike = number | string | {number?: unknown; Number?: unknown; title?: unknown; Title?: unknown; hosts?: unknown} | null | undefined;

// Venue is one of a game's venues: its number, the room it is in (title,
// empty for none yet) and who hosts it. On a bout, ownHosts are the bout's
// own; in a game's list, bouts is how many of its bouts sit there.
export interface Venue {
  number: number;
  title: string;
  room?: number;
  hosts?: string;
  ownHosts?: string;
  bouts?: number;
}

// Room is one of the fest's rooms, with the games whose venues are in it.
export interface Room {
  id: number;
  title: string;
  games?: string[];
}

// VenueLine is what a host changes on one of a game's venues: its room (by
// id, "" for none) and its hosts.
export interface VenueLine {
  room?: string;
  hosts?: string;
}

export function normalizeVenue(venue: VenueLike): Venue | null {
  if (!venue) return null;
  if (typeof venue === "number" || typeof venue === "string") {
    const number = Number(venue);
    return Number.isFinite(number) && number > 0 ? {number, title: ""} : null;
  }
  const number = Number(venue.number ?? venue.Number);
  if (!Number.isFinite(number) || number <= 0) return null;
  const title = String(venue.title ?? venue.Title ?? "").trim();
  const hosts = String(venue.hosts ?? "").trim();
  return {number, title, ...(hosts ? {hosts} : {})};
}

export function formatVenue(venue: VenueLike): string {
  const normalized = normalizeVenue(venue);
  if (!normalized) return "";
  return normalized.title ? `${normalized.number}: ${normalized.title}` : String(normalized.number);
}

export function formatBattleVenue(venue: VenueLike): string {
  const normalized = normalizeVenue(venue);
  if (!normalized) return "";
  return normalized.title
    ? S.widgets.venue.battle(String(normalized.number), normalized.title)
    : S.widgets.venue.battleShort(String(normalized.number));
}

// venueShort is a venue as a bout's head names it: its room when its game
// put it in one, else its number.
export function venueShort(venue: VenueLike): string {
  const normalized = normalizeVenue(venue);
  if (!normalized) return "";
  return normalized.title || S.widgets.venue.battleShort(String(normalized.number));
}

// venuesShort is several venues as a group's head names them.
export function venuesShort(venues: readonly Venue[]): string {
  if (!venues.length) return "";
  if (venues.every((venue) => venue.title)) return venues.map((venue) => venue.title).join(", ");
  return S.widgets.venue.battleShort(venues.map((venue) => venue.number).sort((a, b) => a - b).join(", "));
}

// boutWhere is where and when a bout is played as its head says it: the room
// (or the number) and the start time.
export function boutWhere(venue: VenueLike, startsAt: string | undefined): string {
  const normalized = normalizeVenue(venue);
  return normalized ? withStartsAt(venueShort(normalized), startsAt) : "";
}

export function formatBattleVenueShort(venue: VenueLike): string {
  const normalized = normalizeVenue(venue);
  return normalized ? S.widgets.venue.battleShort(String(normalized.number)) : "";
}

// refusalNotice is the line under a host's table that says why the server
// refused a write; refused shows a reason, or hides the line for "".
function refusalNotice(): {notice: HTMLElement; refused: (error: string) => void} {
  const notice = document.createElement("p");
  notice.className = "hint hint-danger";
  notice.hidden = true;
  return {notice, refused: (error: string) => {
    notice.textContent = error;
    notice.hidden = !error;
  }};
}

export interface RoomsTableOptions {
  editable?: boolean;
  onRename?: (id: number, title: string) => void;
  // onAdd and onDelete resolve to why the server refused, or to "".
  onAdd?: (title: string) => Promise<string>;
  onDelete?: (id: number) => Promise<string>;
}

// buildRoomsTable is the fest's rooms: for a host, each title to rename and a
// delete for one no game's venue is in, and the add row under them.
export function buildRoomsTable(rooms: readonly Room[], options: RoomsTableOptions = {}): HTMLElement {
  const editable = Boolean(options.editable);
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper venues-results-wrapper";
  const {notice, refused} = refusalNotice();
  const title = (room: Room) => {
    const onRename = editable ? options.onRename : undefined;
    if (!onRename) return room.title;
    const input = document.createElement("input");
    input.className = "venue-input";
    input.value = room.title;
    input.setAttribute("aria-label", S.widgets.venue.nameColumn());
    input.addEventListener("change", () => {
      const typed = input.value.trim();
      if (!typed || typed === room.title) {
        input.value = room.title;
        return;
      }
      onRename(room.id, typed);
    });
    return td(input);
  };
  const remove = (room: Room) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "action-icon";
    const games = room.games || [];
    // A room in use says which games use it: they are where to change it.
    const why = games.length ? S.widgets.venue.roomInUse(games.join(", ")) : S.widgets.venue.delete();
    button.title = why;
    button.setAttribute("aria-label", why);
    button.appendChild(icon("trash-2"));
    button.disabled = games.length > 0;
    button.addEventListener("click", () => void options.onDelete?.(room.id).then(refused));
    const box = document.createElement("span");
    box.className = "venue-actions u-row u-gap-xs u-justify-end";
    box.appendChild(button);
    return td(box);
  };
  const deletable = editable && Boolean(options.onDelete);
  const columns: StandingsColumn[] = [{label: S.widgets.venue.nameColumn(), kind: "name"}];
  if (deletable) columns.push({label: ""});
  wrapper.appendChild(standingsTable({
    className: "venues-results-table",
    columns,
    rows: rooms.map((room) => deletable ? [title(room), remove(room)] : [title(room)]),
  }));
  const onAdd = editable ? options.onAdd : undefined;
  if (onAdd) wrapper.appendChild(roomAddForm((name) => void onAdd(name).then(refused)));
  if (editable) wrapper.appendChild(notice);
  return wrapper;
}

// roomAddForm is the row under the host's rooms table that adds a room.
function roomAddForm(onAdd: (title: string) => void): HTMLElement {
  const form = document.createElement("form");
  form.className = "u-row u-wrap u-gap-sm u-align-center";
  const title = document.createElement("input");
  title.type = "text";
  title.className = "input";
  title.size = TITLE_FIELD_SIZE;
  title.placeholder = S.widgets.venue.addTitle();
  title.setAttribute("aria-label", S.widgets.venue.addTitle());
  title.dataset.venueAdd = "";
  const add = document.createElement("button");
  add.type = "submit";
  add.className = "btn";
  add.append(...iconed("plus", S.widgets.venue.add()));
  form.append(title, add);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const name = title.value.trim();
    if (!name) {
      title.focus();
      return;
    }
    onAdd(name);
  });
  return form;
}

export interface GameVenuesTableOptions {
  editable?: boolean;
  // rooms are the fest's, which a venue's select offers.
  rooms?: readonly Room[];
  // onChange saves one field the host changed.
  onChange?: (number: number, line: VenueLine) => void;
}

// buildGameVenuesTable is a game's venues: each number, where it is played
// (a select of the fest's rooms for a host) and who hosts it.
export function buildGameVenuesTable(venues: readonly Venue[], options: GameVenuesTableOptions = {}): HTMLElement {
  const onChange = options.editable ? options.onChange : undefined;
  const where = (venue: Venue) => {
    if (!onChange) return venue.title;
    const select = document.createElement("select");
    select.className = "input venue-input";
    select.setAttribute("aria-label", S.widgets.venue.whereColumn());
    select.appendChild(option("", S.widgets.venue.noRoom()));
    for (const room of options.rooms || []) select.appendChild(option(String(room.id), room.title));
    select.value = venue.room ? String(venue.room) : "";
    select.addEventListener("change", () => onChange(venue.number, {room: select.value}));
    return td(select);
  };
  const hosts = (venue: Venue) => {
    if (!onChange) return venue.hosts || "";
    const input = document.createElement("input");
    input.className = "venue-input";
    input.value = venue.hosts || "";
    input.setAttribute("aria-label", S.widgets.venue.hostsColumn());
    input.dataset.committed = input.value;
    input.addEventListener("change", () => {
      const typed = input.value.trim();
      if (typed === input.dataset.committed) return;
      input.dataset.committed = typed;
      onChange(venue.number, {hosts: typed});
    });
    return td(input);
  };
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper venues-results-wrapper";
  wrapper.appendChild(standingsTable({
    className: "venues-results-table",
    columns: [
      {label: S.widgets.venue.numberColumn(), kind: "place"},
      {label: S.widgets.venue.whereColumn(), kind: "name"},
      {label: S.widgets.venue.hostsColumn(), className: "venue-hosts"},
    ],
    rows: venues.map((venue) => [venue.number, where(venue), hosts(venue)]),
  }));
  return wrapper;
}

// GameRef is one of the fest's games as the copy row offers it.
export interface GameRef {
  ref: string;
  title: string;
}

// venueCopyRow is the copy-from-game row: picking a game copies its
// rooms onto this game's venues of the same numbers.
export function venueCopyRow(games: readonly GameRef[], onCopy: (ref: string) => void): HTMLElement {
  const row = document.createElement("label");
  row.className = "u-row u-wrap u-gap-sm u-align-center";
  const select = document.createElement("select");
  select.className = "input";
  select.appendChild(option("", S.widgets.venue.copyPick()));
  for (const game of games) select.appendChild(option(game.ref, game.title));
  select.addEventListener("change", () => {
    if (select.value) onCopy(select.value);
    select.value = "";
  });
  row.append(S.widgets.venue.copyFrom(), select);
  return row;
}

// withStartsAt puts a bout's start time before its venue's wording. A bout
// with no time keeps the wording as it was.
export function withStartsAt(text: string, startsAt: string | undefined): string {
  const time = (startsAt || "").trim();
  return time ? S.widgets.venue.withTime(text, time).trim() : text;
}

// venueLabel is a bout's venue as a header shows it, number and title,
// clipped where its column ends, the whole of it in a popover when it is: the
// one clipped name (name-cell.ts), which the shell measures. text overrides
// the wording (the grid says it its own way).
export function venueLabel(venue: VenueLike, className = "", text = ""): HTMLElement | null {
  const label = text || formatBattleVenue(venue);
  if (!label) return null;
  return nameCell(label, {tag: "span", className: className ? `venue-label ${className}` : "venue-label"});
}

export interface BoutWhereWhenOptions {
  // title names the bout in the dialog.
  title: string;
  venue: VenueLike;
  startsAt?: string;
  // venueAlways: the page shows the venue even when the bout has no start
  // time. A page that never showed venues (brain, hamsa) shows the label only
  // once a time is set, so it looks as it did until somebody types one.
  venueAlways: boolean;
  className: string;
  // host is the pencil's side: the game's venues and the two writes. A
  // spectator gets the label alone.
  host?: {
    venues: Venue[];
    // pickVenue moves the bout and gives it its own hosts ("" for its
    // venue's).
    pickVenue: (number: number, hosts: string) => void;
    saveStartsAt: (time: string, wave: boolean) => void;
  };
}

// boutWhereWhen is where and when a bout is played as its head shows it: the
// start time before the venue, then, for a host, the pencil that changes
// both. Every bout page uses it, so a time set on any format reads the same.
export function boutWhereWhen(options: BoutWhereWhenOptions): HTMLElement[] {
  const nodes: HTMLElement[] = [];
  const startsAt = (options.startsAt || "").trim();
  if (startsAt || options.venueAlways) {
    const label = venueLabel(options.venue, options.className, boutWhere(options.venue, startsAt));
    if (label) nodes.push(label);
  }
  const host = options.host;
  if (host) {
    const edit = document.createElement("button");
    edit.type = "button";
    edit.className = "btn btn-xs venue-edit-button";
    edit.title = S.ek.venue.edit();
    edit.setAttribute("aria-label", S.ek.venue.edit());
    edit.replaceChildren(icon("pencil"));
    const venue = options.venue && typeof options.venue === "object" ? options.venue as Venue : null;
    edit.addEventListener("click", () => openVenueDialog({
      title: options.title,
      venues: host.venues,
      current: normalizeVenue(options.venue)?.number || 0,
      ownHosts: venue?.ownHosts || "",
      onPick: host.pickVenue,
      startsAt: {current: startsAt, onSave: host.saveStartsAt},
    }));
    nodes.push(edit);
  }
  return nodes;
}

export interface VenueDialogOptions {
  title: string;
  venues: Venue[];
  current: number;
  // ownHosts are the bout's own hosts, "" when it has its venue's.
  ownHosts: string;
  onPick: (number: number, hosts: string) => void;
  // startsAt, when given, adds the bout's start time to the dialog: the
  // time it has now, and what to do with a changed one (wave: every bout of
  // the same round).
  startsAt?: {current: string; onSave: (time: string, wave: boolean) => void};
}

// openVenueDialog asks which of its game's venues a bout is played at, and
// who hosts it when not the venue's hosts.
export function openVenueDialog(options: VenueDialogOptions): void {
  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog venue-dialog";
  const form = document.createElement("form");
  form.className = "venue-dialog-form";
  const title = document.createElement("h2");
  title.textContent = options.title;
  form.appendChild(title);
  // A game with no venues has only the start time to set.
  const select = document.createElement("select");
  select.className = "venue-dialog-select";
  for (const venue of options.venues) {
    select.appendChild(option(String(venue.number), formatVenue(venue)));
  }
  select.value = options.current > 0 ? String(options.current) : "";
  // The bout's own hosts, over its venue's, which the field shows when empty.
  const hostsField = document.createElement("label");
  hostsField.className = "field";
  const hostsName = document.createElement("span");
  hostsName.textContent = S.ek.venue.hosts();
  const hosts = document.createElement("input");
  hosts.type = "text";
  hosts.className = "input";
  hosts.value = options.ownHosts;
  const venueHosts = () => {
    hosts.placeholder = options.venues.find((venue) => String(venue.number) === select.value)?.hosts || "";
  };
  venueHosts();
  select.addEventListener("change", venueHosts);
  hostsField.append(hostsName, hosts);
  if (options.venues.length > 0) form.append(select, hostsField);
  let time: HTMLInputElement | null = null;
  let wave: HTMLInputElement | null = null;
  if (options.startsAt) {
    const field = document.createElement("label");
    field.className = "field";
    const name = document.createElement("span");
    name.textContent = S.ek.venue.time();
    time = document.createElement("input");
    time.type = "text";
    time.inputMode = "numeric";
    time.className = "input input-narrow";
    time.placeholder = "10:30";
    time.value = options.startsAt.current;
    const hint = document.createElement("span");
    hint.className = "hint";
    hint.textContent = S.ek.venue.timeHint();
    field.append(name, time, hint);
    const all = document.createElement("label");
    all.className = "u-row u-gap-xs u-align-center";
    wave = document.createElement("input");
    wave.type = "checkbox";
    all.append(wave, S.ek.venue.timeWave());
    form.append(field, all);
  }
  const actions = document.createElement("div");
  actions.className = "modal-actions";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "btn";
  cancel.textContent = S.ek.venue.cancel();
  cancel.addEventListener("click", () => dialog.close());
  const save = document.createElement("button");
  save.type = "submit";
  save.className = "btn";
  save.textContent = S.ek.venue.save();
  actions.append(cancel, save);
  form.appendChild(actions);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const number = Number(select.value);
    const typedHosts = hosts.value.trim();
    dialog.close();
    if (number > 0 && (number !== options.current || typedHosts !== options.ownHosts)) options.onPick(number, typedHosts);
    if (options.startsAt && time) {
      const typed = time.value.trim();
      if (typed !== options.startsAt.current || wave?.checked) options.startsAt.onSave(typed, Boolean(wave?.checked));
    }
  });
  dialog.addEventListener("close", () => dialog.remove());
  dialog.appendChild(form);
  document.body.appendChild(dialog);
  dialog.showModal();
  (options.venues.length > 0 ? select : time)?.focus();
}
