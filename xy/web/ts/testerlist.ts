// testerlist.ts — "Tester list" ("Questions tested by" for one tour). The
// test list used to BE this list, one per tour. A board-level Tests panel can
// only say who tested at all, so a tour compiles its own: each person with how
// many of the tour's questions they saw. The ChGK custom names those who tested MOST
// of a tour (they should not play it); someone who saw one or two questions still
// may, skipping what they know — so a flat list cannot serve. tourPicked is that
// rule, shared with the card's "except common testers" line.

import S from "./i18nstrings.js";
import { xyApp } from "./app.js";
import { partialSeen, type SeenQuestion, type SessionMeta, testerNames, whoSaw } from "./sessions.js";
import { nameOf, serializeDeclaration } from "./seen.js";
import { xyCrypto } from "./crypto.js";
import { iconed } from "./icons_gen.js";
import { type Board, type ListPanel, listScope, type PanelShell } from "./panels.js";
import type { BoardCard, BoardList } from "./unlock.js";
import type { Tester } from "./sessions.js";

const { el, errMsg } = xyApp;

export interface TesterList {
  // The names of the people the tour's Tester List names. Undeclared, a tour
  // falls back to the custom: everyone who saw MORE than half its questions.
  tourPicked(list: BoardList): Set<string>;
  panel: ListPanel;
}

export function createTesterList(board: Board, shell: PanelShell, deps: { copyPlain(text: string): Promise<void> }): TesterList {
  // One person and how many of the tour's questions they saw. Counted per
  // person, not per Session (#90): somebody at two sittings that each played
  // half the tour saw all of it, and somebody added to a question by hand saw
  // it without any Session at all.
  interface TourTester { tester: Tester; seen: number }

  function tourCoverage(list: BoardList): { cards: BoardCard[]; rows: TourTester[] } {
    const cards = listScope(board, list).cards.filter((c) => c.kind === "question");
    const seen = new Map<string, TourTester>();
    for (const c of cards) {
      for (const t of board.seenOf(c.id)) {
        const row = seen.get(nameOf(t));
        if (row) row.seen++;
        else seen.set(nameOf(t), { tester: t, seen: 1 });
      }
    }
    return { cards, rows: sortRows([...seen.values()]) };
  }

  function sortRows(rows: TourTester[]): TourTester[] {
    const { players, teams } = testerNames(rows.map((r) => r.tester));
    const order = new Map([...players, ...teams].map((n, i) => [n, i]));
    return rows.sort((a, b) => b.seen - a.seen || (order.get(nameOf(a.tester)) ?? 0) - (order.get(nameOf(b.tester)) ?? 0));
  }

  // A tour's Declaration lives on the board, not in this browser: the preamble
  // ships with the package, so two editors preparing it see one answer.
  function tourScope(list: BoardList): { listId: number | null; groupId: number | null } {
    return list.groupId != null ? { listId: null, groupId: list.groupId } : { listId: list.id, groupId: null };
  }

  // null = this tour has no Declaration and falls back to the custom. An empty
  // array = it declared, and names nobody. A tour declared before schema v26
  // named Sessions; it reads as everyone who was at them.
  function declaredFor(list: BoardList): Tester[] | null {
    const s = tourScope(list);
    const decl = board.state.tourDeclarations.find((d) => d.listId === s.listId && d.groupId === s.groupId);
    if (decl) return decl.names;
    const rows = board.state.tourTesters.filter((d) => d.listId === s.listId && d.groupId === s.groupId);
    if (!rows.length) return null;
    return rows.flatMap((d) => d.sessionId != null ? (board.sessionMeta(d.sessionId) || { testers: [] }).testers : []);
  }

  async function declare(list: BoardList, names: Tester[]): Promise<void> {
    const s = tourScope(list);
    await board.verbs.put("setTourDeclaration", `/api/boards/${board.id}/tour-declaration`, {
      list_id: s.listId, group_id: s.groupId,
      names_enc: await xyCrypto.encField(board.dk(), serializeDeclaration(names)),
    });
    const other = (d: { listId: number | null; groupId: number | null }): boolean => d.listId !== s.listId || d.groupId !== s.groupId;
    board.state.tourTesters = board.state.tourTesters.filter(other);
    board.state.tourDeclarations = board.state.tourDeclarations.filter(other).concat([{ ...s, names }]);
  }

  // Undeclared, a tour falls back to the custom: everyone who saw MORE than half
  // its questions. Shared with the card's "except common testers" line.
  function tourPicked(list: BoardList): Set<string> {
    const declared = declaredFor(list);
    if (declared) return new Set(declared.map(nameOf).filter(Boolean));
    const { cards, rows } = tourCoverage(list);
    return new Set(rows.filter((r) => r.seen * 2 > cards.length).map((r) => nameOf(r.tester)));
  }

  // Numbering runs over the whole export scope (a group numbers across its member
  // lists) and is not always 1..n — a № directive can set a number outright.
  function seenQuestions(list: BoardList): SeenQuestion[] {
    const { cards, numbers } = listScope(board, list);
    const out: SeenQuestion[] = [];
    cards.forEach((card, i) => {
      const num = numbers[i];
      if (!num) return;
      const testers = board.seenOf(card.id);
      if (testers.length) out.push({ num, testers });
    });
    return out;
  }

  function openTesterList(list: BoardList): void {
    const box = el("div");
    const { cards, rows } = tourCoverage(list);
    const total = cards.length;
    const picked = tourPicked(list);
    // A declared person who no longer saw anything still gets a row, so the tick
    // can be taken off.
    for (const t of declaredFor(list) || []) {
      if (!rows.some((r) => nameOf(r.tester) === nameOf(t))) rows.push({ tester: t, seen: 0 });
    }

    const line = el("p", { class: "sess-invite" });
    const partial = el("p", { class: "sess-invite" });
    const pickedTesters = (): Tester[] => rows.filter((r) => picked.has(nameOf(r.tester))).map((r) => r.tester);
    const redraw = (): void => {
      const testers = pickedTesters();
      const names = whoSaw(testers.length ? [{ testers } as SessionMeta] : []);
      line.textContent = names ? S.board.testerlist.summary(names) : S.board.testerlist.summaryEmpty();
      partial.textContent = partialSeen(seenQuestions(list), picked);
      partial.hidden = !partial.textContent;
    };

    box.replaceChildren();
    if (!rows.length) box.append(el("p", { class: "label-empty", text: S.board.testerlist.empty() }));
    for (const r of rows) {
      const name = nameOf(r.tester);
      const cb = el("input", { class: "input", type: "checkbox" }) as HTMLInputElement;
      cb.checked = picked.has(name);
      cb.addEventListener("change", () => {
        if (cb.checked) picked.add(name); else picked.delete(name);
        void declare(list, pickedTesters()).catch((err) => {
          shell.message(errMsg(err));
        });
        redraw();
      });
      box.append(el("label", { class: "sess-row" },
        el("div", { class: "sess-head" }, cb, el("span", { class: "sess-title", text: name })),
        el("span", { class: "sess-meta", text: S.board.testerlist.seen(String(r.seen), String(total)) })));
    }
    const copy = el("button", {
      class: "input", type: "button",
      onclick: () => {
        const text = [line.textContent, partial.textContent].filter(Boolean).join("\n");
        void deps.copyPlain(text);
      },
    }, ...iconed("clipboard", S.board.actions.copyText()));
    box.append(el("div", { class: "sess-invite-box" },
      el("div", { class: "sess-invite-lines" }, line, partial), copy));
    redraw();
    shell.open({ icon: "users", title: S.board.testerlist.name(), body: el("div", {},
      el("p", { class: "hint", text: S.board.testerlist.hint() }), box) });
  }


  return {
    tourPicked,
    panel: {
      id: "tester-list", menu: "list", icon: "users",
      label: S.board.testerlist.name(),
      offered: (scope) => scope.cards.some((c) => c.kind === "question"),
      open: (scope) => openTesterList(scope.list),
    },
  };
}
