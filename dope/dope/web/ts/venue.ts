// Venue: the number-plus-title a match is played at, and the venues table.

import {option, td} from "./cells.js";
import {markNameOverflow} from "./widgets.js";
import {standingsTable} from "./standings.js";
import type {StandingsColumn} from "./standings.js";
import {icon, iconed} from "./icons_gen.js";
import S from "./i18nstrings.js";

export type VenueLike = number | string | {number?: unknown; Number?: unknown; title?: unknown; Title?: unknown} | null | undefined;

export interface Venue {
  number: number;
  title: string;
  // bouts is how many bouts play at it, in the fest's venue list.
  bouts?: number;
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
  return {number, title};
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

export function formatBattleVenueShort(venue: VenueLike): string {
  const normalized = normalizeVenue(venue);
  return normalized ? S.widgets.venue.battleShort(String(normalized.number)) : "";
}

export interface VenuesTableOptions {
  editable?: boolean;
  onTitleChange?: (number: number, title: string) => void;
  // onAdd adds a venue. number is 0 when the host left it empty, and then
  // the server takes the next free one. It and onDelete resolve to why the
  // server refused, or to an empty string; the table shows the refusal.
  onAdd?: (title: string, number: number) => Promise<string>;
  onDelete?: (number: number) => Promise<string>;
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
  const onTitleChange = typeof options.onTitleChange === "function" ? options.onTitleChange : null;
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

  const title = (venue: Venue) => {
    if (!editable || !onTitleChange) return venue.title;
    const input = document.createElement("input");
    input.className = "venue-input";
    input.value = venue.title;
    input.dataset.committedTitle = venue.title;
    input.addEventListener("change", () => {
      const title = input.value.trim();
      if (!title) {
        input.value = input.dataset.committedTitle ?? "";
        return;
      }
      if (title === input.dataset.committedTitle) return;
      input.dataset.committedTitle = title;
      onTitleChange(venue.number, title);
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
    remove.addEventListener("click", () => void onDelete?.(venue.number).then(refused));
    box.appendChild(remove);
    return td(box);
  };
  const columns: StandingsColumn[] = [{label: "№", kind: "place"}, {label: S.widgets.venue.nameColumn(), kind: "name"}];
  if (onDelete) columns.push({label: ""});
  wrapper.appendChild(standingsTable({
    className: "venues-results-table",
    columns,
    rows: (venues || []).map((venue) => onDelete ? [venue.number, title(venue), actions(venue)] : [venue.number, title(venue)]),
  }));
  if (onAdd) wrapper.appendChild(venueAddForm(nextVenueNumber(venues), (title, number) => void onAdd(title, number).then(refused)));
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
  title.size = 24;
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
  return time ? S.widgets.venue.withTime(time, text).trim() : text;
}

// venueLabel is a bout's venue as a header shows it, number and title,
// clipped where its column ends, the whole of it in a popover when
// it is. text overrides the wording (the grid says it its own way).
export function venueLabel(venue: VenueLike, className = "", text = ""): HTMLElement | null {
  const label = text || formatBattleVenue(venue);
  if (!label) return null;
  const wrap = document.createElement("span");
  wrap.className = className ? `venue-label ${className}` : "venue-label";
  const name = document.createElement("span");
  name.className = "venue-label-name";
  name.textContent = label;
  name.tabIndex = 0;
  name.setAttribute("aria-label", label);
  const popover = document.createElement("span");
  popover.className = "popover venue-label-popover";
  popover.textContent = label;
  wrap.appendChild(name);
  wrap.appendChild(popover);
  return wrap;
}

// VENUE_POPOVER_SPEC is the floating popover a page binds for its venue
// labels: a clipped one shows its whole title on hover or focus, the way a
// team's name does.
export const VENUE_POPOVER_SPEC = {trigger: ".venue-label-truncated", popover: ".venue-label-popover", anchor: ".venue-label-name"};

// markVenueOverflow flags the venue labels their column clips.
export function markVenueOverflow(root: ParentNode | null | undefined): void {
  markNameOverflow(root, {cellSelector: ".venue-label", nameSelector: ".venue-label-name", truncatedClass: "venue-label-truncated"});
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
    const label = venueLabel(options.venue, options.className, withStartsAt(formatBattleVenue(options.venue), startsAt));
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
