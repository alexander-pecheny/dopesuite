// The fest's venues page (/fest/{ref}/venues, ADR-0037): the fest's rooms,
// and under each what is played there, bout by bout in the order of their
// start times, across the games whose venues their maps put in it. A host
// adds, renames and deletes the rooms here; which game venue is in which room
// is set on each game's own venues tab.

import {buildRoomsTable, formatBattleVenueShort} from "./venue.js";
import type {Room} from "./venue.js";
import {standingsTable} from "./standings.js";
import {td} from "./cells.js";
import {request} from "./team-editor.js";
import S from "./i18nstrings.js";

export interface RoomBout {
  room: number;
  venue: number;
  startsAt?: string;
  game: string;
  gameTitle: string;
  code: string;
  letter?: string;
  title?: string;
  finished?: boolean;
  hosts?: string;
}

interface RoomsPage {
  rooms: Room[];
  bouts: RoomBout[];
  editable: boolean;
}

// minutesOf reads a start time as the host typed it («9:40», «18:00»); one it
// cannot read goes last.
export function minutesOf(time: string | undefined): number {
  const match = /^(\d{1,2})[:.](\d{2})/.exec((time || "").trim());
  return match ? Number(match[1]) * 60 + Number(match[2]) : Number.MAX_SAFE_INTEGER;
}

// byRoom groups the bouts by room, each room's in the order they start; bouts
// with the same time keep the server's order (the game's, then the
// schedule's), since the sort is stable.
export function byRoom(bouts: readonly RoomBout[]): Map<number, RoomBout[]> {
  const out = new Map<number, RoomBout[]>();
  for (const bout of bouts) out.set(bout.room, [...(out.get(bout.room) || []), bout]);
  for (const list of out.values()) list.sort((a, b) => minutesOf(a.startsAt) - minutesOf(b.startsAt));
  return out;
}

(() => {
  const mount = document.getElementById("festVenues");
  const api = mount?.dataset.api || "";
  const games = mount?.dataset.games || "";
  if (!mount || !api) return;

  const boutLink = (bout: RoomBout): HTMLElement => {
    const link = document.createElement("a");
    link.className = "group-round-link";
    link.href = `${games}${encodeURIComponent(bout.game)}/${bout.letter ? `#@${encodeURIComponent(bout.letter)}` : ""}`;
    link.textContent = bout.letter || bout.title || bout.code;
    return td(link);
  };

  // gameCell is the bout's game, and under it the game's venue number and
  // who hosts the bout.
  const gameCell = (bout: RoomBout): HTMLElement => {
    const box = document.createElement("span");
    box.className = "u-col";
    const line = document.createElement("span");
    line.className = "hint";
    line.textContent = [formatBattleVenueShort(bout.venue), bout.hosts].filter(Boolean).join(" · ");
    box.append(bout.gameTitle, line);
    return td(box);
  };

  const schedule = (room: Room, bouts: RoomBout[]): HTMLElement => {
    const section = document.createElement("section");
    section.className = "u-col u-gap-xs";
    const head = document.createElement("h3");
    head.className = "subhead";
    head.textContent = room.title;
    section.appendChild(head);
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

  const render = (data: RoomsPage) => {
    const edit = async (answer: Promise<{ok: boolean; message: string}>): Promise<string> => {
      const result = await answer;
      if (!result.ok) return result.message;
      await load();
      return "";
    };
    const table = !data.editable ? null : buildRoomsTable(data.rooms, {
      editable: true,
      onRename: (id: number, title: string) => void edit(request(`${api}/rooms/${id}`, "PUT", {title})),
      onAdd: (title: string) => edit(request(`${api}/rooms`, "POST", {title})),
      onDelete: (id: number) => edit(request(`${api}/rooms/${id}`, "DELETE")),
    });
    const grouped = byRoom(data.bouts);
    const schedules = document.createElement("div");
    schedules.className = "venue-schedules";
    for (const room of data.rooms) schedules.appendChild(schedule(room, grouped.get(room.id) || []));
    mount.replaceChildren(...(table ? [table] : []), schedules);
  };

  async function load(): Promise<void> {
    const answer = await request(`${api}/rooms`, "GET");
    if (answer.ok) render(answer.data as RoomsPage);
  }
  void load();
})();

export {};
