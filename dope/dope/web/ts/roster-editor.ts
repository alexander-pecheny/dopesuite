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

// suggestLabel is «Фамилия Имя Отчество (id)», the line the suggest shows.
export function suggestLabel(p: RosterPlayer): string {
  const name = fullName(p);
  return p.player_id > 0 ? `${name} (${p.player_id})` : name;
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

type Doc = Document;

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

async function fetchPlayers(query: string): Promise<RosterPlayer[]> {
  const response = await fetch(`/api/buff/players?q=${encodeURIComponent(query)}`, {
    headers: { Accept: "application/json" },
  });
  if (!response.ok) return [];
  const rows = (await response.json()) as
    | Array<{ id: number; surname: string; name: string; patronymic: string }>
    | null;
  if (!Array.isArray(rows)) return [];
  return rows.map((r) => ({
    player_id: r.id,
    surname: r.surname ?? "",
    name: r.name ?? "",
    patronymic: r.patronymic ?? "",
    captain: false,
  }));
}

// mountRosterEditor draws the editor over one [data-roster-editor] container.
export function mountRosterEditor(container: HTMLElement): void {
  const field = container.querySelector<HTMLInputElement>("[data-roster-json]");
  if (!field) return;
  let players = parseRoster(field.value);
  if (players.length === 0) players = [emptyPlayer()];
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
    if (player.player_id > 0 || fullName(player) === "") {
      row.append(drawSuggest(player, index));
    }
    if (player.player_id === 0) {
      row.append(drawTyped(player, index));
    }
    row.append(drawCaptain(player, index), drawRemove(index));
    return row;
  };

  const drawSuggest = (player: RosterPlayer, index: number): HTMLElement => {
    const box = el("div", "u-col u-gap-sm");
    const query = input("input", suggestLabel(player), "Фамилия");
    const list = el("div", "u-col u-gap-sm");
    let timer = 0;
    query.addEventListener("input", () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        void fetchPlayers(query.value.trim()).then((found) => {
          list.textContent = "";
          for (const candidate of found.slice(0, 8)) {
            const pick = document.createElement("button");
            pick.type = "button";
            pick.className = "btn btn-ghost";
            pick.textContent = suggestLabel(candidate);
            pick.addEventListener("click", () => {
              players[index] = { ...candidate, captain: players[index].captain };
              draw();
            });
            list.append(pick);
          }
          const manual = document.createElement("button");
          manual.type = "button";
          manual.className = "btn btn-ghost";
          manual.textContent = "нет в базе";
          manual.addEventListener("click", () => {
            players[index] = { ...emptyPlayer(), surname: query.value.trim(), captain: players[index].captain };
            draw();
          });
          list.append(manual);
        });
      }, 200);
    });
    box.append(query, list);
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
      const node = input("input", String(player[key] ?? ""), placeholder);
      node.addEventListener("input", () => {
        players[index] = { ...players[index], [key]: node.value };
        sync();
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

export function mountRosterEditors(doc: Doc): void {
  doc.querySelectorAll<HTMLElement>("[data-roster-editor]").forEach(mountRosterEditor);
}

if (typeof document !== "undefined") {
  mountRosterEditors(document);
}
