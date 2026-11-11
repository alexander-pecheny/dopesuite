// The fest's venues page (/fest/{ref}/venues): the venues every Game of the
// fest shares, with the title (the room) and the hosts of each, and under
// them what each venue plays, bout by bout in the order of their start times.
// A host edits the venues here; a bout's venue is still moved from the bout
// itself.

import {buildVenuesTable, nextVenueNumber} from "./venue.js";
import type {Venue, VenueFields} from "./venue.js";
import {standingsTable} from "./standings.js";
import {td} from "./cells.js";
import {request} from "./team-editor.js";
import S from "./i18nstrings.js";

interface VenueBout {
  venue: number;
  startsAt?: string;
  game: string;
  gameTitle: string;
  code: string;
  letter?: string;
  title?: string;
  finished?: boolean;
  // room and hosts are the Game's own title and hosts for the venue.
  room?: string;
  hosts?: string;
}

interface Schedule {
  venues: Venue[];
  bouts: VenueBout[];
  editable: boolean;
}

// minutesOf reads a start time as the host typed it («9:40», «18:00»); one it
// cannot read goes last.
export function minutesOf(time: string | undefined): number {
  const match = /^(\d{1,2})[:.](\d{2})/.exec((time || "").trim());
  return match ? Number(match[1]) * 60 + Number(match[2]) : Number.MAX_SAFE_INTEGER;
}

// byVenue groups the bouts by venue, each venue's in the order they start;
// bouts with the same time keep the server's order (the Game's, then the
// schedule's), since the sort is stable.
export function byVenue(bouts: readonly VenueBout[]): Map<number, VenueBout[]> {
  const out = new Map<number, VenueBout[]>();
  for (const bout of bouts) out.set(bout.venue, [...(out.get(bout.venue) || []), bout]);
  for (const list of out.values()) list.sort((a, b) => minutesOf(a.startsAt) - minutesOf(b.startsAt));
  return out;
}

(() => {
  const mount = document.getElementById("festVenues");
  const api = mount?.dataset.api || "";
  const games = mount?.dataset.games || "";
  if (!mount || !api) return;

  const boutLink = (bout: VenueBout): HTMLElement => {
    const link = document.createElement("a");
    link.className = "group-round-link";
    link.href = `${games}${encodeURIComponent(bout.game)}/${bout.letter ? `#@${encodeURIComponent(bout.letter)}` : ""}`;
    link.textContent = bout.letter || bout.title || bout.code;
    return td(link);
  };

  // gameCell is the bout's Game, and under it the venue's title and hosts
  // when the Game gave its own.
  const gameCell = (bout: VenueBout): string | HTMLElement => {
    const own = [bout.room, bout.hosts].filter(Boolean).join(" · ");
    if (!own) return bout.gameTitle;
    const box = document.createElement("span");
    box.className = "u-col";
    const line = document.createElement("span");
    line.className = "hint";
    line.textContent = own;
    box.append(bout.gameTitle, line);
    return td(box);
  };

  const schedule = (venue: Venue, bouts: VenueBout[]): HTMLElement => {
    const section = document.createElement("section");
    section.className = "u-col u-gap-xs";
    const head = document.createElement("h3");
    head.className = "subhead";
    head.textContent = venue.gameTitle
      ? S.widgets.venue.scheduleHeadGame(String(venue.number), venue.title, venue.gameTitle)
      : S.widgets.venue.scheduleHead(String(venue.number), venue.title);
    section.appendChild(head);
    if (venue.hosts) {
      const line = document.createElement("p");
      line.className = "hint";
      line.textContent = venue.hosts;
      section.appendChild(line);
    }
    if (!bouts.length) {
      const empty = document.createElement("p");
      empty.className = "hint";
      empty.textContent = S.widgets.venue.scheduleEmpty();
      section.appendChild(empty);
      return section;
    }
    const wrapper = document.createElement("div");
    wrapper.className = "results-wrapper";
    wrapper.appendChild(standingsTable({
      className: "venue-schedule-table",
      columns: [
        {label: S.widgets.venue.scheduleTime(), kind: "num"},
        {label: S.widgets.venue.scheduleGame(), kind: "name"},
        {label: S.widgets.venue.scheduleBout(), kind: "num"},
      ],
      rows: bouts.map((bout) => [bout.startsAt || "", gameCell(bout), boutLink(bout)]),
    }));
    section.appendChild(wrapper);
    return section;
  };

  const render = (data: Schedule) => {
    const edit = async (answer: Promise<{ok: boolean; message: string}>): Promise<string> => {
      const result = await answer;
      if (!result.ok) return result.message;
      await load();
      return "";
    };
    // A host edits the venues in their table; a spectator reads each one's
    // title and hosts over its bouts.
    // A Game's own venues are edited on that Game's venues tab.
    const table = !data.editable ? null : buildVenuesTable(data.venues.filter((venue) => !venue.gameOnly), {
      editable: true,
      nextNumber: nextVenueNumber(data.venues),
      onChange: (number: number, fields: VenueFields) => void edit(request(`${api}/venues/${number}`, "PUT", fields)),
      onAdd: (title: string, number: number) => edit(request(`${api}/venues`, "POST", number > 0 ? {title, number} : {title})),
      onDelete: (number: number) => edit(request(`${api}/venues/${number}`, "DELETE")),
    });
    const grouped = byVenue(data.bouts);
    const schedules = document.createElement("div");
    schedules.className = "venue-schedules";
    for (const venue of data.venues) schedules.appendChild(schedule(venue, grouped.get(venue.number) || []));
    mount.replaceChildren(...(table ? [table] : []), schedules);
  };

  async function load(): Promise<void> {
    const answer = await request(`${api}/venues/schedule`, "GET");
    if (answer.ok) render(answer.data as Schedule);
  }
  void load();
})();
