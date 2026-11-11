// Venue: the number-plus-title a match is played at (the title is usually the
// room), who hosts there, and the venues table.

import {option, td} from "./cells.js";
import {nameCell} from "./name-cell.js";
import {standingsTable} from "./standings.js";
import type {StandingsColumn} from "./standings.js";
import {icon, iconed} from "./icons_gen.js";
import S from "./i18nstrings.js";

// Visible width, in characters, of the new venue's title field.
const TITLE_FIELD_SIZE = 24;

export type VenueLike = number | string | {number?: unknown; Number?: unknown; title?: unknown; Title?: unknown; hosts?: unknown} | null | undefined;

export interface Venue {
  number: number;
  title: string;
  // hosts are who host there; it may be empty.
  hosts?: string;
  // bouts is how many bouts play at it, in the fest's venue list.
  bouts?: number;
  // In a Game's venue list, title and hosts are what the Game shows; the
  // fest's are festTitle and festHosts, and the Game's own, when it gave
  // them, ownTitle and ownHosts.
  festTitle?: string;
  festHosts?: string;
  ownTitle?: string;
  ownHosts?: string;
  // hidden: the Game does not play at this fest venue. gameOnly: the venue
  // is one Game's own; the fest's list names that Game in gameTitle.
  hidden?: boolean;
  gameOnly?: boolean;
  gameTitle?: string;
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

// VenueFields are what a host edits on a venue; a write names the ones it
// changes.
export interface VenueFields {
  title?: string;
  hosts?: string;
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

// A fest may name its bouts' venues by their titles (the rooms) instead of
// their numbers; the page that draws its bouts says which, from its fest view.
let roomLabels = false;

export function nameVenuesByRoom(on: boolean): void {
  roomLabels = on;
}

export function venuesByRoom(): boolean {
  return roomLabels;
}

// venueShort is a venue as a bout's head names it: its number (venue 1), or its
// title when the fest names venues by room and the venue has one.
export function venueShort(venue: VenueLike): string {
  const normalized = normalizeVenue(venue);
  if (!normalized) return "";
  return roomLabels && normalized.title ? normalized.title : S.widgets.venue.battleShort(String(normalized.number));
}

// venuesShort is several venues as a group's head names them.
export function venuesShort(venues: readonly Venue[]): string {
  if (!venues.length) return "";
  if (roomLabels && venues.every((venue) => venue.title)) return venues.map((venue) => venue.title).join(", ");
  return S.widgets.venue.battleShort(venues.map((venue) => venue.number).sort((a, b) => a - b).join(", "));
}

// boutWhere is where and when a bout is played as its head says it: the
// table's number, the start time, then the venue's title, usually its room
// (venue 1 18:00, room 308).
export function boutWhere(venue: VenueLike, startsAt: string | undefined): string {
  const normalized = normalizeVenue(venue);
  if (!normalized) return "";
  const where = withStartsAt(venueShort(normalized), startsAt);
  return normalized.title && !roomLabels ? S.widgets.venue.withTitle(where, normalized.title) : where;
}

export function formatBattleVenueShort(venue: VenueLike): string {
  const normalized = normalizeVenue(venue);
  return normalized ? S.widgets.venue.battleShort(String(normalized.number)) : "";
}

export interface VenuesTableOptions {
  editable?: boolean;
  // onChange saves one field the host changed.
  onChange?: (number: number, fields: VenueFields) => void;
  // onAdd adds a venue. number is 0 when the host left it empty, and then
  // the server takes the next free one. It and onDelete resolve to why the
  // server refused, or to an empty string; the table shows the refusal.
  onAdd?: (title: string, number: number) => Promise<string>;
  onDelete?: (number: number) => Promise<string>;
  // inherit edits a Game's own title and hosts over the fest's: a field
  // holds the Game's own value with the fest's as its placeholder, and an
  // emptied one goes back to the fest's.
  inherit?: boolean;
  // onHide, with inherit, says whether the Game plays at a fest venue.
  onHide?: (number: number, hidden: boolean) => void;
  // nextNumber is the number the add row offers; by default the one after
  // the table's highest.
  nextNumber?: number;
}

// nextVenueNumber is the number a new venue takes when the host gives none:
// the one after the fest's highest, as the server picks it.
export function nextVenueNumber(venues: readonly Venue[] | null | undefined): number {
  return (venues || []).reduce((top, venue) => Math.max(top, Number(venue.number) || 0), 0) + 1;
}

// venueDeletable says whether a venue can be deleted: no bout plays at it.
export function venueDeletable(venue: Venue): boolean {
  return !(Number(venue.bouts) > 0);
}

export function buildVenuesTable(venues: Venue[] | null | undefined, options: VenuesTableOptions = {}): HTMLElement {
  const editable = Boolean(options.editable);
  const onChange = editable && typeof options.onChange === "function" ? options.onChange : null;
  const onDelete = editable && typeof options.onDelete === "function" ? options.onDelete : null;
  const onAdd = editable && typeof options.onAdd === "function" ? options.onAdd : null;
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper venues-results-wrapper";
  const notice = document.createElement("p");
  notice.className = "hint hint-danger";
  notice.hidden = true;
  const refused = (error: string) => {
    notice.textContent = error;
    notice.hidden = !error;
  };

  // field is one of a venue's texts: an input for a host, the text for a
  // spectator. The title may not be emptied; the hosts may.
  const inherit = Boolean(options.inherit);
  const field = (venue: Venue, key: keyof VenueFields, label: string) => {
    if (!onChange) return venue[key] || "";
    // A Game's own venue has no fest values under it: it is edited as is.
    const over = inherit && !venue.gameOnly;
    const value = (over ? venue[key === "title" ? "ownTitle" : "ownHosts"] : venue[key]) || "";
    const input = document.createElement("input");
    input.className = "venue-input";
    input.value = value;
    if (over) input.placeholder = venue[key === "title" ? "festTitle" : "festHosts"] || "";
    input.setAttribute("aria-label", label);
    input.dataset.committed = value;
    input.addEventListener("change", () => {
      const typed = input.value.trim();
      if (!typed && key === "title" && !over) {
        input.value = input.dataset.committed ?? "";
        return;
      }
      if (typed === input.dataset.committed) return;
      input.dataset.committed = typed;
      onChange(venue.number, {[key]: typed});
    });
    return td(input);
  };
  const actions = (venue: Venue) => {
    const box = document.createElement("span");
    box.className = "venue-actions u-row u-gap-xs u-justify-end";
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "action-icon";
    remove.title = S.widgets.venue.delete();
    remove.setAttribute("aria-label", S.widgets.venue.delete());
    remove.appendChild(icon("trash-2"));
    remove.disabled = !venueDeletable(venue);
    // A Game deletes only its own venues; the fest's go on the fest's page.
    if (inherit && !venue.gameOnly) remove.hidden = true;
    remove.addEventListener("click", () => void onDelete?.(venue.number).then(refused));
    box.appendChild(remove);
    return td(box);
  };
  const columns: StandingsColumn[] = [
    {label: "№", kind: "place"},
    {label: S.widgets.venue.nameColumn(), kind: "name"},
    {label: S.widgets.venue.hostsColumn(), className: "venue-hosts"},
  ];
  const onHide = inherit && typeof options.onHide === "function" ? options.onHide : null;
  // used is a fest venue's tick on a Game's tab: whether the Game plays there.
  const used = (venue: Venue) => {
    if (!onHide || venue.gameOnly) return "";
    const box = document.createElement("input");
    box.type = "checkbox";
    box.checked = !venue.hidden;
    box.setAttribute("aria-label", S.widgets.venue.usedColumn());
    box.addEventListener("change", () => onHide(venue.number, !box.checked));
    return td(box);
  };
  if (onHide) columns.push({label: S.widgets.venue.usedColumn(), kind: "num"});
  if (onDelete) columns.push({label: ""});
  wrapper.appendChild(standingsTable({
    className: "venues-results-table",
    columns,
    rows: (venues || []).map((venue) => {
      const row = [
        venue.number,
        field(venue, "title", S.widgets.venue.nameColumn()),
        field(venue, "hosts", S.widgets.venue.hostsColumn()),
      ];
      const full = onHide ? [...row, used(venue)] : row;
      return onDelete ? [...full, actions(venue)] : full;
    }),
  }));
  if (onAdd) wrapper.appendChild(venueAddForm(options.nextNumber || nextVenueNumber(venues), (title, number) => void onAdd(title, number).then(refused)));
  if (onAdd || onDelete) wrapper.appendChild(notice);
  return wrapper;
}

// venueAddForm is the row under the host's venues table that adds a venue:
// a number, which may stay empty for the next free one, and a title.
function venueAddForm(next: number, onAdd: (title: string, number: number) => void): HTMLElement {
  const form = document.createElement("form");
  form.className = "u-row u-wrap u-gap-sm u-align-center";
  const number = document.createElement("input");
  number.type = "number";
  number.min = "1";
  number.step = "1";
  number.inputMode = "numeric";
  number.className = "input input-narrow";
  number.placeholder = String(next);
  number.title = S.widgets.venue.addNumber();
  number.setAttribute("aria-label", S.widgets.venue.addNumber());
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
  form.append(number, title, add);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const name = title.value.trim();
    if (!name) {
      title.focus();
      return;
    }
    const wanted = Math.trunc(Number(number.value));
    onAdd(name, Number.isFinite(wanted) && wanted > 0 ? wanted : 0);
  });
  return form;
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
  // host is the pencil's side: the fest's venues and the two writes. A
  // spectator gets the label alone.
  host?: {
    venues: Venue[];
    pickVenue: (number: number) => void;
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
    edit.addEventListener("click", () => openVenueDialog({
      title: options.title,
      venues: host.venues,
      current: normalizeVenue(options.venue)?.number || 0,
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
  onPick: (number: number) => void;
  // startsAt, when given, adds the bout's start time to the dialog: the
  // time it has now, and what to do with a changed one (wave: every bout of
  // the same round).
  startsAt?: {current: string; onSave: (time: string, wave: boolean) => void};
}

// openVenueDialog asks which of the fest's venues a bout is played at.
export function openVenueDialog(options: VenueDialogOptions): void {
  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog venue-dialog";
  const form = document.createElement("form");
  form.className = "venue-dialog-form";
  const title = document.createElement("h2");
  title.textContent = options.title;
  form.appendChild(title);
  // A fest with no venues has only the start time to set.
  const select = document.createElement("select");
  select.className = "venue-dialog-select";
  for (const venue of options.venues) {
    select.appendChild(option(String(venue.number), venue.title ? `${venue.number}: ${venue.title}` : String(venue.number)));
  }
  select.value = options.current > 0 ? String(options.current) : "";
  if (options.venues.length > 0) form.appendChild(select);
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
    dialog.close();
    if (number > 0 && number !== options.current) options.onPick(number);
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
