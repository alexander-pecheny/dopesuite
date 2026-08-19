// testerlist.ts — «Список тестеров» («Вопросы тестировали» for one tour). The
// test list used to BE this list, one per tour. A board-level Тесты panel can
// only say who tested at all, so a tour compiles its own: each session with how
// many of the tour's questions it saw. The ЧГК custom names those who tested MOST
// of a tour (they should not play it); someone who saw one or two questions still
// may, skipping what they know — so a flat list cannot serve. tourPicked is that
// rule, shared with the card's «кроме общих тестеров» line.

import { xyApp } from "./app.js";
import { partialSeen, type SeenQuestion, type SessionMeta, whoSaw } from "./sessions.js";
import { iconed } from "./icons_gen.js";
import { type Board, type ListPanel, listScope, type PanelShell } from "./panels.js";
import type { BoardCard, BoardList } from "./unlock.js";
import type { Tester } from "./sessions.js";

const { el, errMsg } = xyApp;

export interface TesterList {
  // Undeclared, a tour falls back to the custom: everyone who saw MORE than
  // half its questions.
  tourPicked(list: BoardList): Set<number>;
  panel: ListPanel;
}

export function createTesterList(board: Board, shell: PanelShell, deps: { copyPlain(text: string): Promise<void> }): TesterList {
  interface TourTester { id: number; name: string; seen: number }

  function tourCoverage(list: BoardList): { cards: BoardCard[]; rows: TourTester[] } {
    const cards = listScope(board, list).cards.filter((c) => c.kind === "question");
    const seen = new Map<number, number>();
    for (const c of cards) {
      for (const sid of board.playingsOf(c.id)) seen.set(sid, (seen.get(sid) || 0) + 1);
    }
    const rows = [...seen.entries()]
      .map(([id, n]): TourTester => ({ id, name: board.sessionName(id), seen: n }))
      .sort((a, b) => b.seen - a.seen || a.name.localeCompare(b.name, "ru"));
    return { cards, rows };
  }

  // Which sessions were ticked last time, per tour. A personal working state on
  // the way to a document, so it lives beside the other display prefs rather than
  // on the server.
  // A tour's Declaration lives on the board, not in this browser: the preamble
  // ships with the package, so two editors preparing it see one answer. The ticks
  // used to sit in localStorage, where they outlived the sessions they named.
  function tourScope(list: BoardList): { listId: number | null; groupId: number | null } {
    return list.groupId != null ? { listId: null, groupId: list.groupId } : { listId: list.id, groupId: null };
  }

  // null = this tour has no Declaration and falls back to the custom. An empty
  // array = it declared, and names nobody.
  function declaredFor(list: BoardList): number[] | null {
    const s = tourScope(list);
    const rows = board.state.tourTesters.filter((d) => d.listId === s.listId && d.groupId === s.groupId);
    if (!rows.length) return null;
    return rows.filter((d) => d.sessionId != null).map((d) => d.sessionId as number);
  }

  async function declare(list: BoardList, ids: number[]): Promise<void> {
    const s = tourScope(list);
    await board.verbs.put("setTourTesters", `/api/boards/${board.id}/tour-testers`, {
      list_id: s.listId, group_id: s.groupId, session_ids: ids,
    });
    const rest = board.state.tourTesters.filter((d) => d.listId !== s.listId || d.groupId !== s.groupId);
    board.state.tourTesters = ids.length
      ? rest.concat(ids.map((sessionId) => ({ ...s, sessionId })))
      : rest.concat([{ ...s, sessionId: null }]);
  }

  // Undeclared, a tour falls back to the custom: everyone who saw MORE than half
  // its questions. Shared with the card's «кроме общих тестеров» line.
  function tourPicked(list: BoardList): Set<number> {
    const { cards, rows } = tourCoverage(list);
    const declared = declaredFor(list);
    return new Set(declared ?? rows.filter((r) => r.seen * 2 > cards.length).map((r) => r.id));
  }

  // Numbering runs over the whole export scope (a group numbers across its member
  // lists) and is not always 1..n — a № directive can set a number outright.
  function seenQuestions(list: BoardList): SeenQuestion[] {
    const { cards, numbers } = listScope(board, list);
    const out: SeenQuestion[] = [];
    cards.forEach((card, i) => {
      const num = numbers[i];
      if (!num) return;
      const testers = board.playingsOf(card.id).flatMap((sid) => (board.sessionMeta(sid) || { testers: [] }).testers || []);
      if (testers.length) out.push({ num, testers });
    });
    return out;
  }

  function openTesterList(list: BoardList): void {
    const box = el("div");
    const { cards, rows } = tourCoverage(list);
    const total = cards.length;
    const picked = tourPicked(list);

    const line = el("p", { class: "sess-invite" });
    const partial = el("p", { class: "sess-invite" });
    const redraw = (): void => {
      const testers: Tester[] = [];
      for (const r of rows) {
        if (!picked.has(r.id)) continue;
        const m = board.sessionMeta(r.id);
        if (m) testers.push(...m.testers);
      }
      const names = whoSaw(testers.length ? [{ testers } as SessionMeta] : []);
      line.textContent = names ? `Вопросы тестировали: ${names}.` : "Никто не отмечен.";
      partial.textContent = partialSeen(seenQuestions(list), new Set(testers.map((t) => (t.text || "").trim())));
      partial.hidden = !partial.textContent;
    };

    box.replaceChildren();
    if (!rows.length) box.append(el("p", { class: "label-empty", text: "Вопросы этого тура никто не тестировал." }));
    for (const r of rows) {
      const cb = el("input", { class: "input", type: "checkbox" }) as HTMLInputElement;
      cb.checked = picked.has(r.id);
      cb.addEventListener("change", () => {
        if (cb.checked) picked.add(r.id); else picked.delete(r.id);
        void declare(list, [...picked]).catch((err) => {
          shell.message(errMsg(err));
        });
        redraw();
      });
      box.append(el("label", { class: "sess-row" },
        el("div", { class: "sess-head" }, cb, el("span", { class: "sess-title", text: r.name })),
        el("span", { class: "sess-meta", text: `${r.seen} из ${total}` })));
    }
    const copy = el("button", {
      class: "input", type: "button",
      onclick: () => {
        const text = [line.textContent, partial.textContent].filter(Boolean).join("\n");
        void deps.copyPlain(text);
      },
    }, ...iconed("clipboard", "Скопировать"));
    box.append(el("div", { class: "sess-invite-box" },
      el("div", { class: "sess-invite-lines" }, line, partial), copy));
    redraw();
    shell.open({ icon: "users", title: "Список тестеров", body: el("div", {},
      el("p", { class: "hint", text: "По умолчанию отмечены те, кто видел больше половины вопросов из списка." }), box) });
  }


  return {
    tourPicked,
    panel: {
      id: "tester-list", menu: "list", icon: "users",
      label: "Список тестеров",
      open: (scope) => openTesterList(scope.list),
    },
  };
}
