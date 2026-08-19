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
// with the cards concatenated in board order and the title the file will carry.
export interface ListScope {
  list: BoardList;
  grouped: boolean;
  group: BoardGroup | null;
  lists: BoardList[];
  cards: BoardCard[];
  title: string;
}

export function listScope(board: Board, list: BoardList): ListScope {
  let lists = [list], title = list.title || "export", group: BoardGroup | null = null;
  if (list.groupId != null) {
    lists = board.listsInGroup(list.groupId);
    group = board.groupById(list.groupId) || null;
    if (group && group.name) title = group.name;
  }
  return { list, grouped: list.groupId != null, group, lists, cards: lists.flatMap((l) => board.cardsOf(l.id)), title };
}

interface Entry<S> {
  id: string;
  icon: IconName;
  label: string | ((scope: S) => string);
  title?: string;
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
  onClick(): void;
}

const registry: Panel[] = [];

export function registerPanel(...panels: Panel[]): void {
  for (const p of panels) {
    if (registry.some((r) => r.id === p.id)) throw new Error(`panel ${p.id} registered twice`);
    registry.push(p);
  }
}

// The two menus, as data, in registration order.
export function boardMenu(): PanelMenuItem[] {
  return registry.filter((p): p is BoardPanel => p.menu === "board").map((p) => item(p, undefined));
}
export function listMenu(scope: ListScope): PanelMenuItem[] {
  return registry.filter((p): p is ListPanel => p.menu === "list" && (!p.offered || p.offered(scope))).map((p) => item(p, scope));
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
