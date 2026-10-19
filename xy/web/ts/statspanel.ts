// statspanel.ts — the add-stats action: chgksuite's add_stats for a tour. The
// server reads the tournament's results and hands back one line per question
// (internal/server/stats.go); this copies the list, or the group it belongs
// to, and appends each line to its question card's comment. The tour the
// reader started from stays as it was.

import S from "./i18nstrings.js";
import { xyApp } from "./app.js";
import { xyChgk } from "./chgk.js";
import { xySync } from "./sync.js";
import { modal } from "./modal.js";
import { buildBundle } from "./bundleexport.js";
import { applyBundle } from "./bundleapply.js";
import type { Bundle } from "./bundle.js";
import type { Transfer } from "./transfer.js";
import type { Board, ListPanel, ListScope } from "./panels.js";

const { byId, errMsg } = xyApp;

const CLOSE_AFTER_SUCCESS_MS = 1200;

// withStatsLine is stats.patchQuestion on a card's 4s: the line becomes the
// comment, another paragraph of it, or another item when the comment is a list.
export function withStatsLine(desc: string, line: string): string {
  const f = xyChgk.splitFields(desc);
  const comment = (f.comment ?? "").trimEnd();
  const last = comment.split("\n").pop() ?? "";
  if (!comment.trim()) f.comment = line;
  else if (/^\s*- /.test(last)) f.comment = `${comment}\n- ${line}`;
  else f.comment = `${comment}\n${line}`;
  return xyChgk.composeFields(f);
}

// statsQuestions are the cards the results count, in order: every numbered
// question but a warm-up, whose number starts with 0.
export function statsQuestions(scope: Pick<ListScope, "cards" | "numbers">): number[] {
  return scope.cards.filter((_, i) => {
    const n = scope.numbers[i];
    return n != null && !n.startsWith("0");
  }).map((c) => c.id);
}

// questionRange is the default for the results range field: the numbers this
// tour's questions carry, which is what to read off a whole tournament's table.
function questionRange(scope: ListScope): string {
  const nums = scope.numbers.filter((n): n is string => n != null && /^[1-9]\d*$/.test(n));
  return nums.length ? `${nums[0]}-${nums[nums.length - 1]}` : "";
}

export function createStatsPanel(board: Board, transfer: Transfer): ListPanel {
  const statsModal = modal("stats");
  let current: ListScope | null = null;
  const msg = (): HTMLElement => byId("statsMessage");
  const field = (id: string): HTMLInputElement => byId<HTMLInputElement>(id);

  function open(scope: ListScope): void {
    current = scope;
    field("statsRange").value = questionRange(scope);
    msg().textContent = "";
    statsModal.open({ onClose: () => { current = null; } });
  }

  async function fetchLines(count: number): Promise<{ lines: string[]; warnings: string[] }> {
    const fd = new FormData();
    fd.append("count", String(count));
    fd.append("rating_ids", field("statsRatingIds").value.trim());
    fd.append("question_range", field("statsRange").value.trim());
    fd.append("team_naming_threshold", field("statsThreshold").value.trim());
    const file = field("statsFile").files?.[0];
    if (file) fd.append("results", file, file.name);
    const res = await fetch("/api/stats/lines", { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    return (await res.json()) as { lines: string[]; warnings: string[] };
  }

  // patchBundle puts each line on its card and names the copy. The Bundle keeps
  // the source's ids, so a card is found by the id it has on the board.
  function patchBundle(bundle: Bundle, byCard: Map<number, string>): void {
    for (const c of bundle.cards) {
      const line = byCard.get(c.id);
      if (line !== undefined) c.description = withStatsLine(c.description, line);
    }
    for (const l of bundle.lists) l.title = S.board.stats.copyTitle(l.title);
    for (const g of bundle.groups) g.name = S.board.stats.copyTitle(g.name);
  }

  async function run(): Promise<void> {
    const scope = current;
    if (!scope || !xySync.requireOnline(S.board.stats.offline(), msg())) return;
    const btn = byId<HTMLButtonElement>("statsRun");
    btn.disabled = true;
    const log = (line: string): void => { msg().textContent = line; };
    try {
      const ids = statsQuestions(scope);
      log(S.board.stats.reading());
      const { lines, warnings } = await fetchLines(ids.length);
      const listIds = scope.grouped ? scope.lists.map((l) => l.id) : [scope.list.id];
      const { bundle, bytesOf } = await buildBundle(board, listIds, log);
      patchBundle(bundle, new Map(ids.map((id, i) => [id, lines[i]])));
      // The copy lands right after the tour it was made from.
      const ranks = board.state.lists.map((l) => l.rank).sort();
      const after = board.state.lists.filter((l) => listIds.includes(l.id)).map((l) => l.rank).sort().pop() ?? null;
      const ctx = await transfer.loadMoveBoard(board.id);
      const result = await applyBundle(bundle, {
        boardId: board.id,
        dk: board.dk(),
        append: {
          labels: ctx.labels.map((l) => ({ id: l.id, name: l.name, color: l.color })),
          sessions: ctx.sessions.map((x) => ({ id: x.id, meta: x.meta })),
          lastRank: after,
          nextRank: ranks.find((r) => after !== null && r > after) ?? null,
          sourceName: board.state.name,
        },
      }, bytesOf, log);
      if (result.failed) throw new Error(result.units.find((u) => u.error)!.error);
      await board.reload();
      board.render();
      log([S.board.stats.done(), ...warnings].join("\n"));
      if (!warnings.length) setTimeout(statsModal.close, CLOSE_AFTER_SUCCESS_MS);
    } catch (err) {
      log(errMsg(err));
    } finally {
      btn.disabled = false;
    }
  }

  byId("statsForm").addEventListener("submit", (e) => { e.preventDefault(); void run(); });

  return {
    id: "stats", menu: "list", icon: "percent",
    label: (scope) => scope.grouped ? S.board.stats.labelGrouped() : S.board.stats.label(),
    title: S.board.stats.menuTitle(),
    offered: (scope) => scope.game !== "si" && statsQuestions(scope).length > 0,
    open,
  };
}
