// Venue: the number-plus-title a match is played at, and the venues table.

import {option, td} from "./cells.js";
import {markNameOverflow} from "./widgets.js";
import {standingsTable} from "./standings.js";
import S from "./i18nstrings.js";

export type VenueLike = number | string | {number?: unknown; Number?: unknown; title?: unknown; Title?: unknown} | null | undefined;

export interface Venue {
  number: number;
  title: string;
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
}

export function buildVenuesTable(venues: Venue[] | null | undefined, options: VenuesTableOptions = {}): HTMLElement {
  const editable = Boolean(options.editable);
  const onTitleChange = typeof options.onTitleChange === "function" ? options.onTitleChange : null;
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper venues-results-wrapper";

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
  wrapper.appendChild(standingsTable({
    className: "venues-results-table",
    columns: [{label: "№", kind: "place"}, {label: S.widgets.venue.nameColumn(), kind: "name"}],
    rows: (venues || []).map((venue) => [venue.number, title(venue)]),
  }));
  return wrapper;
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

export interface VenueDialogOptions {
  title: string;
  venues: Venue[];
  current: number;
  onPick: (number: number) => void;
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
  const select = document.createElement("select");
  select.className = "venue-dialog-select";
  for (const venue of options.venues) {
    select.appendChild(option(String(venue.number), venue.title ? `${venue.number}: ${venue.title}` : String(venue.number)));
  }
  select.value = options.current > 0 ? String(options.current) : "";
  form.appendChild(select);
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
  });
  dialog.addEventListener("close", () => dialog.remove());
  dialog.appendChild(form);
  document.body.appendChild(dialog);
  dialog.showModal();
  select.focus();
}
