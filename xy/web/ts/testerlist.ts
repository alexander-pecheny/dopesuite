// testerlist.ts — "Tester list" ("Questions tested by" for one tour). The
// test list used to BE this list, one per tour. A board-level Tests panel can
// only say who tested at all, so a tour compiles its own: each person with how
// many of the tour's questions they saw. The ChGK custom names those who tested MOST
// of a tour (they should not play it); someone who saw one or two questions still
// may, skipping what they know — so a flat list cannot serve. tourPicked is that
// rule, shared with the card's "except common testers" line.

import S from "./i18nstrings.js";
import { xyApp } from "./app.js";
import { copyName, partialSeen, type SeenQuestion, type SessionMeta, testerNames, whoSaw } from "./sessions.js";
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

  // The tests the tour's questions were played at, most questions first, each
  // with how many of them it played.
  function tourSessions(list: BoardList): Array<{ id: number; played: number; testers: Tester[] }> {
    const cards = listScope(board, list).cards.filter((c) => c.kind === "question");
    const played = new Map<number, number>();
    for (const c of cards) for (const sid of board.playingsOf(c.id)) played.set(sid, (played.get(sid) || 0) + 1);
    return [...played.entries()]
      .map(([id, n]) => ({ id, played: n, testers: (board.sessionMeta(id) || { testers: [] }).testers || [] }))
      .sort((a, b) => b.played - a.played || a.id - b.id);
  }

  // orderedRows is the order people are read in: surname, then given name.
  function orderedRows(rows: TourTester[]): TourTester[] {
    const { players, teams } = testerNames(rows.map((r) => r.tester));
    const order = new Map([...players, ...teams].map((n, i) => [n, i]));
    return [...rows].sort((a, b) => (order.get(nameOf(a.tester)) ?? 0) - (order.get(nameOf(b.tester)) ?? 0));
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

    // Most people saw a tour at a test, and a test is ticked or not as a whole,
    // so the rows are the tests: one checkbox for everyone who was there, and
    // the names under it open to a checkbox each for the exceptions. People who
    // saw the tour only outside any test are listed one by one after them. A
    // person at two tests is under both, and the two boxes stay in step.
    const byName = new Map(rows.map((r) => [nameOf(r.tester), r]));
    const boxes: Array<{ cb: HTMLInputElement; names: string[] }> = [];
    const sync = (): void => {
      for (const b of boxes) {
        const on = b.names.filter((n) => picked.has(n)).length;
        b.cb.checked = on > 0 && on === b.names.length;
        b.cb.indeterminate = on > 0 && on < b.names.length;
      }
    };
    const pick = (names: string[], on: boolean): void => {
      for (const n of names) if (on) picked.add(n); else picked.delete(n);
      sync();
      void declare(list, pickedTesters()).catch((err) => {
        shell.message(errMsg(err));
      });
      redraw();
    };
    const checkbox = (names: string[]): HTMLInputElement => {
      const cb = el("input", { type: "checkbox" }) as HTMLInputElement;
      cb.addEventListener("change", () => pick(names, cb.checked));
      boxes.push({ cb, names });
      return cb;
    };
    const personRow = (r: TourTester): HTMLElement => el("label", { class: "sess-row" },
      el("div", { class: "sess-head" }, checkbox([nameOf(r.tester)]), el("span", { class: "sess-title", text: nameOf(r.tester) })),
      el("span", { class: "sess-meta", text: S.board.testerlist.seen(String(r.seen), String(total)) }));

    const inATest = new Set<string>();
    for (const g of tourSessions(list)) {
      const members = orderedRows(g.testers.map((t) => byName.get(nameOf(t))).filter((r): r is TourTester => r != null));
      if (!members.length) continue;
      const names = members.map((r) => nameOf(r.tester));
      names.forEach((n) => inATest.add(n));
      box.append(el("div", { class: "sess-row" },
        el("label", { class: "sess-head" }, checkbox(names), el("span", { class: "sess-title", text: board.sessionName(g.id) })),
        el("span", { class: "sess-meta", text: S.board.testerlist.seen(String(g.played), String(total)) }),
        el("details", { class: "testerlist-members" },
          el("summary", { class: "sess-meta", text: members.map((r) => copyName(r.tester)).join(", ") }),
          el("div", { class: "u-col" }, ...members.map(personRow)))));
    }
    const alone = rows.filter((r) => !inATest.has(nameOf(r.tester)));
    if (alone.length) {
      if (inATest.size) box.append(el("p", { class: "section-label", text: S.board.testerlist.outside() }));
      for (const r of alone) box.append(personRow(r));
    }
    sync();
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
