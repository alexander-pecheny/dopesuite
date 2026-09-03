// Venue: the number-plus-title a match is played at, and the venues table.

import {td} from "./cells.js";
import {standingsTable} from "./standings.js";
import S from "./i18nstrings_ru_gen.js";

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
