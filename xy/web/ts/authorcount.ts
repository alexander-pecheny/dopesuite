// authorcount.ts — «Счётчик авторов»: per author, how many of a tour's questions
// up to a chosen number carry their name (count) and their 1/n split of
// co-authored ones (share, shown as a percentage of the tour) — the share is
// what a fee is divided by.

import { xyChgk, type ChgkCard } from "./chgk.js";
import { xyVersions } from "./versions.js";
import { xyFind } from "./find.js";
import { xyApp } from "./app.js";
import { iconed } from "./icons_gen.js";
import type { ListPanel, PanelShell } from "./panels.js";

const { el } = xyApp;

export interface AuthorRow { name: string; count: number; share: number; numbers: string[] }
export interface AuthorCount {
  rows: AuthorRow[];
  unauthored: AuthorRow | null;
  questions: number;
  hasZero: boolean;
  cutoffFound: boolean;
}

// Stress marks, spaces, case and ё/е are how one name gets typed on two cards,
// not two people; the row shows the most common spelling, stress marks dropped.
function authorKey(name: string): string {
  return xyFind.foldSearch(name).replace(/\s+/g, " ").trim();
}
function displayName(name: string): string {
  return name.replace(/\p{Mn}/gu, "").replace(/\s+/g, " ").trim();
}

function authorsOf(card: ChgkCard): string[] {
  const names = xyChgk.splitFields(xyVersions.versionBody(card.desc, 0)).authors || [];
  return names.filter((n) => authorKey(n));
}

export function countAuthors(cards: ReadonlyArray<ChgkCard>, upTo: string, includeZero: boolean): AuthorCount {
  const numbers = xyChgk.numberQuestionCards(cards);
  const questions: Array<{ card: ChgkCard; number: string }> = [];
  cards.forEach((card, i) => { const n = numbers[i]; if (n != null) questions.push({ card, number: n }); });
  const hasZero = questions.some((q) => xyChgk.isZeroNumber(q.number));
  let end = -1;
  questions.forEach((q, i) => { if (q.number === upTo.trim()) end = i; });
  const picked = questions.slice(0, end + 1).filter((q) => includeZero || !xyChgk.isZeroNumber(q.number));

  const tally = new Map<string, { spellings: Map<string, number>; row: AuthorRow }>();
  const unauthored: AuthorRow = { name: "без автора", count: 0, share: 0, numbers: [] };
  for (const q of picked) {
    const names = authorsOf(q.card);
    if (!names.length) { unauthored.count++; unauthored.share++; unauthored.numbers.push(q.number); continue; }
    const share = 1 / names.length;
    for (const name of names) {
      const key = authorKey(name);
      let t = tally.get(key);
      if (!t) { t = { spellings: new Map(), row: { name: "", count: 0, share: 0, numbers: [] } }; tally.set(key, t); }
      const shown = displayName(name);
      t.spellings.set(shown, (t.spellings.get(shown) || 0) + 1);
      t.row.count++;
      t.row.share += share;
      t.row.numbers.push(q.number);
    }
  }
  const rows: AuthorRow[] = [];
  for (const t of tally.values()) {
    let best = "", bestN = 0;
    for (const [s, n] of t.spellings) if (n > bestN) { best = s; bestN = n; }
    rows.push({ ...t.row, name: best });
  }
  rows.sort((a, b) => b.count - a.count || b.share - a.share || a.name.localeCompare(b.name, "ru"));
  return { rows, unauthored: unauthored.count ? unauthored : null, questions: picked.length, hasZero, cutoffFound: end >= 0 };
}

export function formatShare(share: number, questions: number): string {
  return questions ? `${Math.round((share / questions) * 1000) / 10}%` : "";
}

export const xyAuthorCount = { countAuthors, formatShare };

// The panel: a cutoff field, a «считать нулевые» toggle shown only when there
// are нулевые, and the table — the 1/n split is what a fee is divided by, and
// нулевые are usually not paid.
export function createAuthorCountPanel(shell: PanelShell, deps: { copyPlain(text: string): Promise<void> }): ListPanel {
  return {
    id: "author-count", menu: "list", icon: "calculator",
    label: "Счётчик авторов",
    open(scope) {
      const cards = scope.cards;
      const numbers = xyChgk.numberQuestionCards(cards).filter((n): n is string => n != null);
      const upTo = el("input", { class: "input", type: "text", placeholder: "номер", inputmode: "numeric", autocomplete: "off", spellcheck: "false", value: numbers[numbers.length - 1] || "" }) as HTMLInputElement;
      const zero = el("input", { type: "checkbox" }) as HTMLInputElement;
      const zeroRow = el("label", { class: "attach-lossless" }, zero, " считать нулевые");
      const box = el("div", { class: "author-count" });

      const redraw = (): void => {
        const r = countAuthors(cards, upTo.value, zero.checked);
        zeroRow.hidden = !r.hasZero;
        box.replaceChildren();
        if (!r.cutoffFound) { box.append(el("p", { class: "label-empty", text: "Нет такого вопроса." })); return; }
        const rows = r.unauthored ? [...r.rows, r.unauthored] : r.rows;
        const cells = (row: AuthorRow): string[] =>
          [row.name, String(row.count), formatShare(row.share, r.questions), row.numbers.join(", ")];
        const total = ["Всего", String(rows.reduce((n, row) => n + row.count, 0)), formatShare(r.questions, r.questions), ""];
        const tr = (vals: string[], tag: "th" | "td", cls?: string): HTMLElement =>
          el("tr", cls ? { class: cls } : {}, ...vals.map((v) => el(tag, { text: v })));
        box.append(el("table", { class: "data-table author-count-table" },
          el("thead", {}, tr(["Автор", "Вопросов", "Доля", "Номера"], "th")),
          el("tbody", {}, ...rows.map((row) => tr(cells(row), "td")), tr(total, "td", "author-count-total"))));
        const copy = el("button", { class: "input", type: "button", onclick: () => {
          void deps.copyPlain([...rows.map(cells), total].map((v) => v.join("\t")).join("\n"));
        } }, ...iconed("clipboard", "Скопировать"));
        box.append(el("div", { class: "sess-invite-box" }, copy));
      };
      upTo.oninput = redraw;
      zero.onchange = redraw;
      redraw();

      shell.open({ icon: "users", title: "Счётчик авторов", body: el("div", {},
        el("div", { class: "u-row u-gap-sm u-align-center u-wrap" },
          el("span", { class: "muted", text: "До вопроса" }), upTo,
          el("span", { class: "muted", text: "включительно" }), zeroRow),
        box) });
    },
  };
}
