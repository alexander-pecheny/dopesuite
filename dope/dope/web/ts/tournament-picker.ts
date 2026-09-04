// The tournament picker's list, narrowed and ordered in the page. A week's
// worth of tournaments is a few dozen cards, all of them already rendered, and
// a Representative runs through several orderings before settling on one — so
// none of this asks the server again.

export interface Card {
  id: string;
  difficulty: number;
  teams: number;
  kind: string;
  keep: boolean;
}

export interface Controls {
  kind: string;
  from: number | null;
  to: number | null;
  sort: string;
}

// shows says whether a card survives the filters. A tournament whose editors
// forecast no difficulty is not hidden by a difficulty bound: it is unknown,
// not easy.
export function shows(card: Card, c: Controls): boolean {
  if (c.kind !== "all" && card.kind !== c.kind) return false;
  if (card.difficulty <= 0) return true;
  if (c.from !== null && card.difficulty < c.from) return false;
  if (c.to !== null && card.difficulty > c.to) return false;
  return true;
}

// order is the list as it should read: what is still in play first, in the
// chosen order, and everything ticked off after it. A tournament with no
// forecast sorts last either way — it cannot be compared, so it does not
// pretend to be the easiest.
export function order(cards: Card[], c: Controls): Card[] {
  const rank = (card: Card): number => {
    if (c.sort === "teams-desc") return -card.teams;
    if (card.difficulty <= 0) return Number.POSITIVE_INFINITY;
    return c.sort === "difficulty-desc" ? -card.difficulty : card.difficulty;
  };
  return [...cards].sort((a, b) => {
    if (a.keep !== b.keep) return a.keep ? -1 : 1;
    const d = rank(a) - rank(b);
    return Number.isNaN(d) ? 0 : d;
  });
}

const num = (raw: string): number => {
  const n = Number(raw);
  return Number.isFinite(n) ? n : 0;
};

function readCard(row: HTMLElement): Card {
  const keep = row.querySelector<HTMLInputElement>("[data-tournament-keep] input");
  return {
    id: row.getAttribute("data-tournament") || "",
    difficulty: num(row.getAttribute("data-difficulty") || ""),
    teams: num(row.getAttribute("data-teams") || ""),
    kind: row.getAttribute("data-kind") || "",
    keep: !keep || keep.checked,
  };
}

function readControls(scope: ParentNode): Controls {
  const field = (name: string): HTMLInputElement | HTMLSelectElement | null =>
    scope.querySelector(`[data-tournament-filter="${name}"]`);
  const bound = (name: string): number | null => {
    const raw = (field(name) as HTMLInputElement | null)?.value.trim() || "";
    return raw === "" ? null : num(raw);
  };
  return {
    kind: field("kind")?.value || "all",
    from: bound("from"),
    to: bound("to"),
    sort: field("sort")?.value || "difficulty-asc",
  };
}

export function mountTournamentPicker(list: HTMLElement, scope: ParentNode): void {
  const rows = new Map<string, HTMLElement>();
  for (const row of list.querySelectorAll<HTMLElement>("[data-tournament]")) {
    rows.set(row.getAttribute("data-tournament") || "", row);
  }
  const draw = (): void => {
    const cards = [...rows.values()].map(readCard);
    const controls = readControls(scope);
    for (const card of order(cards, controls)) {
      const row = rows.get(card.id);
      if (!row) continue;
      row.hidden = !shows(card, controls);
      // A poll offers what the Representative can see: a card the filters took
      // away must not go on the ballot just because its tick survived.
      const keep = row.querySelector<HTMLInputElement>("[data-tournament-keep] input");
      if (keep) keep.disabled = row.hidden;
      list.append(row);
    }
  };
  scope.addEventListener("input", draw);
  scope.addEventListener("change", draw);
  draw();
}

if (typeof document !== "undefined") {
  const list = document.getElementById("tournaments");
  if (list) mountTournamentPicker(list, document);
}
