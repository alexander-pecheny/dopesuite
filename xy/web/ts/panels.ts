// panels.ts — the board's panel registry: what the ☰ and the list ⋯ menus
// offer is a list of entries, each a feature that lives in its own module and
// registers here with a label, an icon and an open(scope). board.ts keeps one
// registerPanel(...) line per feature and renders both menus from the registry.
//
// Board is the seam a panel works through: the live state, the read helpers,
// the four mutation verbs, render and the status dot. A panel that needs more
// (the card detail's cross-board copy, the attachments cache) takes it as its
// own dependency, so the board seam stays what every panel needs and no more.

import type { BoardCard, BoardGroup, BoardList, BoardState, CardLabel } from "./unlock.js";
import type { SessionMeta } from "./sessions.js";
import type { MembersState } from "./boardmembers.js";
import type { MutationVerbs } from "./carddetail.js";
import type { DataKey } from "./crypto.js";
import type { OpBody } from "./store.js";
import type { IconName } from "./icons_gen.js";
import type { WriteState } from "./app.js";
import type { Modal } from "./modal.js";
import { icon } from "./icons_gen.js";
import { xyChgk } from "./chgk.js";

export interface Board {
  readonly id: number;
  readonly state: BoardState & MembersState;
  dk(): DataKey;
  cardsOf(listId: number): BoardCard[];
  listsInGroup(groupId: number): BoardList[];
  groupById(groupId: number): BoardGroup | undefined;
  assignmentsOf(cardId: number, sessionId: number | null | undefined): CardLabel[];
  playingsOf(cardId: number): number[];
  sessionMeta(id: number): SessionMeta | null;
  sessionName(id: number): string;
  verbs: MutationVerbs & { post(kind: string, path: string, body: OpBody): Promise<unknown> };
  render(): void;
  setStatus(op: WriteState): void;
  // Re-read the snapshot from the server after a write the local state cannot
  // replay exactly (a rebuilt list group, an import).
  reload(): Promise<void>;
}

// What a per-list panel works on: the list, or its whole group when it has one,
// with the cards concatenated in board order, their display numbers (one run
// across the group, so list 2 picks up where list 1 left off, № directives
// included) and the title the file will carry.
export interface ListScope {
  list: BoardList;
  grouped: boolean;
  group: BoardGroup | null;
  lists: BoardList[];
  cards: BoardCard[];
  numbers: Array<string | null>;
  title: string;
}

export function listScope(board: Board, list: BoardList): ListScope {
  let lists = [list], title = list.title || "export", group: BoardGroup | null = null;
  if (list.groupId != null) {
    lists = board.listsInGroup(list.groupId);
    group = board.groupById(list.groupId) || null;
    if (group && group.name) title = group.name;
  }
  const cards = lists.flatMap((l) => board.cardsOf(l.id));
  return { list, grouped: list.groupId != null, group, lists, cards, numbers: xyChgk.numberQuestionCards(cards), title };
}

// listNumbers is one list's slice of its scope's numbering, parallel to
// board.cardsOf(list.id) — what the kanban column and the card editor show.
export function listNumbers(board: Board, list: BoardList): Array<string | null> {
  const scope = listScope(board, list);
  let off = 0;
  for (const l of scope.lists) {
    const n = board.cardsOf(l.id).length;
    if (l.id === list.id) return scope.numbers.slice(off, off + n);
    off += n;
  }
  return [];
}

interface Entry<S> {
  id: string;
  icon: IconName;
  label: string | ((scope: S) => string);
  title?: string;
  // Opens a cluster. The rule is drawn above the first entry of the cluster
  // that survives `offered` — not above this one, which may not be offered at
  // all — so a conditional cluster head cannot take the grouping with it.
  divider?: boolean;
  offered?(scope: S): boolean;
  open(scope: S): void;
}
export type BoardPanel = Entry<void> & { menu: "board" };
export type ListPanel = Entry<ListScope> & { menu: "list" };
export type Panel = BoardPanel | ListPanel;

export interface PanelMenuItem {
  id: string;
  icon: IconName;
  label: string;
  title?: string;
  divider?: boolean;
  onClick(): void;
}

const registry: Panel[] = [];

export function registerPanel(...panels: Panel[]): void {
  for (const p of panels) {
    if (registry.some((r) => r.id === p.id)) throw new Error(`panel ${p.id} registered twice`);
    registry.push(p);
  }
}

// The two menus, as data, in registration order. Both honour `offered` — the
// board menu is rebuilt whenever the snapshot lands, because what a reader may
// do with a board is not known until their role is.
export function boardMenu(): PanelMenuItem[] {
  return cluster(registry.filter((p): p is BoardPanel => p.menu === "board"), undefined);
}
export function listMenu(scope: ListScope): PanelMenuItem[] {
  return cluster(registry.filter((p): p is ListPanel => p.menu === "list"), scope);
}

// cluster numbers the entries by the `divider` marks in registration order,
// then drops the ones this scope is not offered — so the rule lands on
// whichever entry of a cluster is left, and never on the first row of the menu.
function cluster<S>(panels: Array<Entry<S> & { menu: string }>, scope: S): PanelMenuItem[] {
  let n = 0;
  const numbered = panels.map((p) => ({ p, n: p.divider ? ++n : n }));
  const out: PanelMenuItem[] = [];
  let last: number | null = null;
  for (const { p, n: mine } of numbered) {
    if (p.offered && !p.offered(scope)) continue;
    const it = item(p, scope);
    if (last !== null && mine !== last) it.divider = true;
    last = mine;
    out.push(it);
  }
  return out;
}

function item<S>(p: Entry<S>, scope: S): PanelMenuItem {
  return {
    id: p.id,
    icon: p.icon,
    label: typeof p.label === "function" ? p.label(scope) : p.label,
    title: p.title,
    onClick: () => p.open(scope),
  };
}

// The tests build the registry afresh.
export function resetPanels(): void { registry.length = 0; }

// The panel shell: one generic modal (board.dopeui's panelOverlay) that a panel
// with an el()-built body renders into, so adding such a panel touches no
// .dopeui, vocab or Go. Title and icon are the panel's; the body is replaced.
export interface PanelShell {
  open(spec: { icon: IconName; title: string; body: Node; onClose?(): void }): void;
  message(text: string): void;
}

export function createPanelShell(m: Modal, nodes: { title: HTMLElement; body: HTMLElement }): PanelShell {
  return {
    open({ icon: name, title, body, onClose }) {
      const glyph = icon(name);
      glyph.classList.add("ico-lead");
      nodes.title.replaceChildren(glyph, title);
      m.el.querySelector<HTMLElement>("[role=dialog]")?.setAttribute("aria-label", title);
      nodes.body.replaceChildren(body);
      m.open({ onClose });
    },
    message: (text) => m.message(text),
  };
}
