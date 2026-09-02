// The Состав editor: rows of a suggest over buff's player mirror, a
// «нет в базе» fallback that turns a row into three typed name fields with id
// 0, and one captain. Flags are derived server-side on save, so the editor
// never offers them. The whole roster travels in one hidden field, which is
// what the заявка form posts.

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
  const hint = games > 0 ? `${games} ${gamesWord(games)} · ${p.player_id}` : String(p.player_id);
  return {value: JSON.stringify(p), label: fullName(p), hint};
}

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

// suggestLabel is «Фамилия Имя Отчество (id)», the line the suggest shows,
// with the games the mirror knows the player by when it does.
export function suggestLabel(p: SuggestedPlayer): string {
  const name = fullName(p);
  if (p.player_id <= 0) return name;
  const games = Number(p.games) || 0;
  return games > 0 ? `${name} (${p.player_id}) · ${games} ${gamesWord(games)}` : `${name} (${p.player_id})`;
}

// gamesWord is «игра/игры/игр» — Russian counts by the last digits.
export function gamesWord(games: number): string {
  const tens = games % 100;
  if (tens >= 11 && tens <= 14) return "игр";
  switch (games % 10) {
    case 1:
      return "игра";
    case 2:
    case 3:
    case 4:
      return "игры";
    default:
      return "игр";
  }
}

// setCaptain keeps exactly one captain: picking a new one clears the old.
export function setCaptain(players: RosterPlayer[], index: number): RosterPlayer[] {
  return players.map((p, i) => ({ ...p, captain: i === index }));
}

// rosterWarning is what the form says about the roster's size; "" when it is
// nothing to remark on.
export function rosterWarning(players: RosterPlayer[]): string {
  const filled = players.filter((p) => fullName(p) !== "");
  if (filled.length > MAX_ROSTER) return `В составе больше ${MAX_ROSTER} игроков.`;
  if (filled.length > 0 && !filled.some((p) => p.captain)) return "Отметьте капитана.";
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

async function fetchPlayers(query: string): Promise<SuggestedPlayer[]> {
  const response = await fetch(`/api/buff/players?q=${encodeURIComponent(query)}`, {
    headers: { Accept: "application/json" },
  });
  if (!response.ok) return [];
  const rows = (await response.json()) as
    | Array<{ id: number; surname: string; name: string; patronymic: string; games?: number }>
    | null;
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

// mountRosterEditor draws the editor over one [data-roster-editor] container.
export function mountRosterEditor(container: HTMLElement): void {
  const field = container.querySelector<HTMLInputElement>("[data-roster-json]");
  if (!field) return;
  let players = parseRoster(field.value);
  // There is always a row waiting for the next player: «Добавить игрока» is
  // for putting one back after a removal, not for every name.
  if (players.length === 0 || fullName(players[players.length - 1]) !== "") players.push(emptyPlayer());
  const rows = el("div", "u-col u-gap-sm");
  const warning = el("p", "hint");
  const add = document.createElement("button");
  add.type = "button";
  add.className = "btn btn-ghost";
  add.textContent = "Добавить игрока";
  container.append(rows, warning, add);

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
    // suggest until «нет в базе» turns it into the three name fields. The name
    // takes the width the row has left, so «Печеный Александр Павлович» fits.
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
  // them is already there and waiting rather than behind «Добавить игрока».
  const openNextRow = (): void => {
    if (fullName(players[players.length - 1]) !== "") {
      appendEmptyRow(true);
      return;
    }
    focusRow(players.length - 1);
  };

  const drawSuggest = (player: RosterPlayer, index: number): HTMLElement => {
    const box = el("div", "u-col u-gap-sm");
    const query = input("input", suggestLabel(player), "Фамилия Имя");
    // Wide enough that the row wraps its controls under it on a phone rather
    // than squeezing the name into a third of the line.
    query.size = 28;
    autocomplete(query, async (typed) => {
      const found = await fetchPlayers(typed);
      const choices = found.map(playerChoice);
      // The mirror does not know a player who has never played; the row says
      // so and turns into three typed fields.
      if (typed.trim() !== "") choices.push({value: MANUAL, label: "нет в базе"});
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
      ["surname", "Фамилия"],
      ["name", "Имя"],
      ["patronymic", "Отчество"],
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
    caption.textContent = "капитан";
    label.append(radio, caption);
    return label;
  };

  const drawRemove = (index: number): HTMLElement => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "btn btn-ghost";
    button.textContent = "Убрать";
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

// mountBuffTeamField names the team a rating id stands for as it is typed,
// which is how a Representative tells 5723 from 5732.
const MANUAL = "\u0000manual";

export function mountBuffTeamField(field: HTMLInputElement): void {
  const hint = document.createElement("p");
  hint.className = "hint";
  field.after(hint);
  let timer = 0;
  const show = (): void => {
    const id = Number(field.value.trim());
    if (!id) {
      hint.textContent = "";
      return;
    }
    void fetchJSON<BuffTeam>(`/api/buff/team/${encodeURIComponent(id)}`).then((team) => {
      hint.textContent = team ? [team.name, team.town].filter(Boolean).join(" · ") : "Буфф не знает такой команды";
    });
  };
  field.addEventListener("input", () => {
    window.clearTimeout(timer);
    timer = window.setTimeout(show, 200);
  });
  show();
}

// mountBuffTournamentField turns the tournament id into a suggest over the
// tournaments buff knows to be playable on the Слот's date.
export function mountBuffTournamentField(field: HTMLInputElement): void {
  const query = document.createElement("input");
  query.type = "text";
  query.className = "input";
  query.autocomplete = "off";
  query.placeholder = "поиск по названию";
  field.after(query);
  const on = field.getAttribute("data-buff-tournament") || "";
  autocomplete(query, async (text) => {
    const rows = await fetchJSON<BuffTournament[]>(
      `/api/buff/tournaments?q=${encodeURIComponent(text)}&on=${encodeURIComponent(on)}`,
    );
    return (rows || []).map((t) => ({value: String(t.id), label: t.name, hint: `${t.type} · ${t.id}`}));
  }, (choice) => {
    field.value = choice.value;
    field.dispatchEvent(new Event("input", {bubbles: true}));
    query.value = choice.label;
  });
}

export function mountRosterEditors(doc: Document): void {
  doc.querySelectorAll<HTMLElement>("[data-roster-editor]").forEach(mountRosterEditor);
  doc.querySelectorAll<HTMLInputElement>("input[data-buff-team]").forEach(mountBuffTeamField);
  doc.querySelectorAll<HTMLInputElement>("input[data-buff-tournament]").forEach(mountBuffTournamentField);
  doc.querySelectorAll<HTMLElement>("[data-rating-venue-load]").forEach(mountRatingVenueLoader);
}

if (typeof document !== "undefined") {
  mountRosterEditors(document);
}

// «Загрузить данные»: rating.chgk.info knows what a venue is called and where,
// and buff does not mirror venues — so the form fetches it on click.
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

export function mountRatingVenueLoader(button: HTMLElement): void {
  const form = button.closest("form");
  if (!form) return;
  const field = (name: string): HTMLInputElement | null =>
    form.querySelector<HTMLInputElement>(`[data-${name}]`);
  const message = document.createElement("p");
  message.className = "hint";
  button.after(message);
  button.addEventListener("click", () => {
    const id = Number((field("rating-venue")?.value || "").trim());
    if (!id) {
      message.textContent = "Сначала укажите id площадки";
      return;
    }
    message.textContent = "Загружаем…";
    void fetch(`/api/rating/venue/${encodeURIComponent(id)}`, {headers: {Accept: "application/json"}})
      .then(async (response) => {
        if (!response.ok) throw new Error((await response.text()).trim());
        return (await response.json()) as {name?: string; city?: string};
      })
      .then((venue) => {
        const name = field("venue-name");
        const city = field("venue-city");
        const slug = form.querySelector<HTMLInputElement>('input[name="slug"]');
        if (name && venue.name) name.value = venue.name;
        if (city && venue.city) city.value = venue.city;
        // A slug the Representative typed is theirs; an empty one is derived.
        if (slug && !slug.value.trim() && venue.name) slug.value = slugify(venue.name);
        message.textContent = "";
      })
      .catch((error: Error) => {
        message.textContent = error.message || "Не удалось загрузить";
      });
  });
}
