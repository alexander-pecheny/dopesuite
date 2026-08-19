// listsmanage.ts — «Управление списками»: one row per list (and a bordered block
// per group). Lists can be reordered by dragging a row or by entering a target
// position; checking several rows lets you move them together or — when the
// checked rows are consecutive, ungrouped lists — link them into a group.
// Orderable units are standalone lists and whole groups; a group always moves as
// one block, keeping its members consecutive (the invariant the board relies on).

import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { xyRank } from "./rank.js";
import { byRank, dragAfterIn } from "./dragrank.js";
import { icon, iconed } from "./icons_gen.js";
import { modal } from "./modal.js";
import type { Board, BoardPanel } from "./panels.js";
import type { BoardList } from "./unlock.js";

const { jpost, jpatch, jdelete, el, byId, errMsg } = xyApp;
const { keyBetween } = xyRank;

export interface Unit { kind: "group" | "list"; id: number; key: string; lists: BoardList[] }

// unitsOf folds an ordered run of lists into orderable units: each maximal run
// of lists sharing a group_id is one group unit; ungrouped lists are singletons.
export function unitsOf(sorted: ReadonlyArray<BoardList>): Unit[] {
  const units: Unit[] = [];
  let i = 0;
  while (i < sorted.length) {
    const l = sorted[i];
    if (l.groupId != null) {
      const gid = l.groupId, run: BoardList[] = [];
      while (i < sorted.length && sorted[i].groupId === gid) { run.push(sorted[i]); i++; }
      units.push({ kind: "group", id: gid, key: "g" + gid, lists: run });
    } else {
      units.push({ kind: "list", id: l.id, key: "l" + l.id, lists: [l] });
      i++;
    }
  }
  return units;
}

export interface ListsManage {
  // Persist a new unit order — the board's column drag folds the DOM into
  // units and writes them through the same rank writer the modal uses.
  applyUnitOrder(units: Unit[]): Promise<void>;
  panel: BoardPanel;
}

export function createListsManage(board: Board): ListsManage {
  const listsManageModal = modal("listsManage");
  const listsManageRows = byId("listsManageRows");
  let manageSelected = new Set<string>();       // selected unit keys ("l"+listId / "g"+groupId)
  let manageUnitByKey = new Map<string, Unit>();      // key → unit (rebuilt each render)
  let manageDragKey: string | null = null;
  let manageDragCommitted = false;
  // Dragging a member row *inside* its group (reorder within, never across):
  // the group id whose members container owns the gesture.
  let memberDragGid: number | null = null;
  let memberDragCommitted = false;

  const computeUnits = (): Unit[] => unitsOf([...board.state.lists].sort(byRank));

  function openListsManage(): void {
    manageSelected = new Set();
    byId<HTMLInputElement>("listsMovePos").value = "";
    listsManageModal.open();
    renderManage();
  }

  function renderManage(): void {
    const units = computeUnits();
    manageUnitByKey = new Map(units.map((u) => [u.key, u]));
    // Drop selections whose units no longer exist (e.g. after a group dissolved).
    for (const k of [...manageSelected]) if (!manageUnitByKey.has(k)) manageSelected.delete(k);
    listsManageRows.replaceChildren();
    units.forEach((u, idx) => listsManageRows.append(renderManageUnit(u, idx + 1)));
    updateManageToolbar(units);
  }

  function manageCheckbox(unit: Unit): HTMLElement {
    const cb = el("input", { type: "checkbox" }) as HTMLInputElement;
    cb.checked = manageSelected.has(unit.key);
    cb.addEventListener("change", () => {
      if (cb.checked) manageSelected.add(unit.key); else manageSelected.delete(unit.key);
      updateManageToolbar(computeUnits());
    });
    return el("label", { class: "lm-check" }, cb);
  }

  function manageMoveControl(unit: Unit): HTMLElement {
    const inp = el("input", { class: "input lm-move-pos", type: "number", min: "1", placeholder: "№" }) as HTMLInputElement;
    const btn = el("button", { class: "btn btn-small btn-ghost lm-move-btn", type: "button", title: "Переместить на эту позицию" }, icon("arrow-up-down"));
    const go = (): void => { const n = parseInt(inp.value, 10); if (n >= 1) void moveUnitsTo(new Set([unit.key]), n); };
    btn.addEventListener("click", go);
    inp.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); go(); } });
    return el("div", { class: "lm-move" }, inp, btn);
  }

  function manageTitle(list: BoardList): string {
    return list.title || "(без названия)";
  }

  function renderManageUnit(unit: Unit, pos: number): HTMLElement {
    const node = el("div", { class: "lm-unit lm-" + unit.kind, draggable: "true", dataset: { unitKey: unit.key } });
    if (unit.kind === "group") {
      const g = board.groupById(unit.id);
      node.append(el("div", { class: "lm-row lm-grouphead" },
        manageCheckbox(unit),
        el("span", { class: "lm-pos", text: "#" + pos }),
        el("span", { class: "lm-handle", text: "≡", title: "Перетащить" }),
        el("span", { class: "lm-title lm-group-title" }, ...iconed("link", (g && g.name) || "Связанные списки")),
        el("button", { class: "lm-icon", type: "button", title: "Переименовать группу", onclick: () => { void renameGroup(unit.id); } }, icon("pencil")),
        el("button", { class: "lm-icon", type: "button", title: "Разъединить группу", onclick: () => { void unlinkGroup(unit.id); } }, icon("unlink")),
        manageMoveControl(unit),
      ));
      // Members are draggable within their own group (the whole group is still
      // the unit that moves among lists — a member can't be dragged out of it,
      // that would break the group's consecutiveness).
      const members = el("div", { class: "lm-members" });
      for (const l of unit.lists) {
        const row = el("div", { class: "lm-member", draggable: "true", dataset: { listId: l.id } },
          el("span", { class: "lm-handle", text: "≡", title: "Перетащить внутри группы" }),
          el("span", { class: "lm-title", text: manageTitle(l) }));
        row.addEventListener("dragstart", (e) => {
          e.stopPropagation(); // the unit node is draggable too — don't start both
          memberDragGid = unit.id;
          memberDragCommitted = false;
          row.classList.add("dragging");
          if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
          try { e.dataTransfer?.setData("text/plain", "m" + l.id); } catch (_) {}
        });
        row.addEventListener("dragend", () => {
          row.classList.remove("dragging");
          memberDragGid = null;
          if (!memberDragCommitted) renderManage(); // aborted drag — resync DOM from state
        });
        members.append(row);
      }
      members.addEventListener("dragover", (e) => {
        if (memberDragGid !== unit.id) return;
        e.preventDefault();
        e.stopPropagation();
        const dragging = members.querySelector(".lm-member.dragging");
        if (!dragging) return;
        const after = dragAfterIn([...members.querySelectorAll(".lm-member:not(.dragging)")], e.clientY);
        if (after == null) members.append(dragging);
        else members.insertBefore(dragging, after);
      });
      members.addEventListener("drop", (e) => {
        if (memberDragGid !== unit.id) return;
        e.preventDefault();
        e.stopPropagation();
        memberDragCommitted = true;
        const byId = new Map(unit.lists.map((l): [string, BoardList] => [String(l.id), l]));
        const order = [...members.querySelectorAll<HTMLElement>(".lm-member")]
          .map((n) => byId.get(n.dataset.listId || ""))
          .filter((l): l is BoardList => !!l);
        if (order.length === unit.lists.length) void applyMemberOrder(unit.key, order);
      });
      node.append(members);
    } else {
      node.append(el("div", { class: "lm-row" },
        manageCheckbox(unit),
        el("span", { class: "lm-pos", text: "#" + pos }),
        el("span", { class: "lm-handle", text: "≡", title: "Перетащить" }),
        el("span", { class: "lm-title", text: manageTitle(unit.lists[0]) }),
        manageMoveControl(unit),
      ));
    }
    node.addEventListener("dragstart", (e) => {
      manageDragKey = unit.key;
      manageDragCommitted = false;
      node.classList.add("dragging");
      if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
      try { e.dataTransfer?.setData("text/plain", unit.key); } catch (_) {}
    });
    node.addEventListener("dragend", () => {
      node.classList.remove("dragging");
      manageDragKey = null;
      if (!manageDragCommitted) renderManage(); // aborted drag — resync DOM from state
    });
    return node;
  }

  function manageDragAfter(y: number): Element | null {
    return dragAfterIn([...listsManageRows.querySelectorAll(".lm-unit:not(.dragging)")], y);
  }

  listsManageRows.addEventListener("dragover", (e) => {
    if (manageDragKey == null) return;
    e.preventDefault();
    const dragging = listsManageRows.querySelector(".lm-unit.dragging");
    if (!dragging) return;
    const after = manageDragAfter(e.clientY);
    if (after == null) listsManageRows.append(dragging);
    else listsManageRows.insertBefore(dragging, after);
  });
  listsManageRows.addEventListener("drop", (e) => {
    if (manageDragKey == null) return;
    e.preventDefault();
    manageDragCommitted = true;
    const order = [...listsManageRows.querySelectorAll<HTMLElement>(".lm-unit")]
      .map((n) => manageUnitByKey.get(n.dataset.unitKey || ""))
      .filter((u): u is Unit => !!u);
    void applyUnitOrder(order);
  });

  function updateManageToolbar(units: Unit[]): void {
    const linkBtn = byId<HTMLButtonElement>("listsLinkBtn");
    const moveBtn = byId<HTMLButtonElement>("listsMoveBtn");
    const selected = units.filter((u) => manageSelected.has(u.key));
    moveBtn.disabled = selected.length === 0;
    // Linking needs ≥2 selected, all ungrouped single lists, consecutive in order.
    let canLink = selected.length >= 2 && selected.every((u) => u.kind === "list");
    if (canLink) {
      const idxs = selected.map((u) => units.indexOf(u)).sort((a, b) => a - b);
      canLink = idxs.every((v, i) => i === 0 || v === idxs[i - 1] + 1);
    }
    linkBtn.disabled = !canLink;
  }

  // applyUnitOrder rewrites list ranks to match the given unit order (groups stay
  // contiguous because their member lists are emitted together). Only changed
  // ranks are patched. Offline-capable (rank patches flow through the sync engine).
  async function applyUnitOrder(orderedUnits: Unit[]): Promise<void> {
    const msg = byId("listsManageMessage");
    const flat = orderedUnits.flatMap((u) => u.lists);
    let r: string | null = null;
    const patches: Array<[BoardList, string]> = [];
    for (const l of flat) { r = keyBetween(r, null); if (l.rank !== r) patches.push([l, r]); }
    if (!patches.length) { renderManage(); return; }
    board.setStatus("saving");
    try {
      for (const [l, rank] of patches) { l.rank = rank; await board.verbs.patch("patchList", `/api/lists/${l.id}`, { rank }); }
      board.setStatus("saved");
      board.render();
      renderManage();
    } catch (err) { board.setStatus("error"); msg.textContent = errMsg(err); void board.reload(); }
  }

  // applyMemberOrder reorders the lists INSIDE one group: the group keeps its
  // place among the units, only its members' ranks are rewritten.
  function applyMemberOrder(unitKey: string, order: BoardList[]): Promise<void> {
    const units = computeUnits();
    const target = units.find((u) => u.key === unitKey);
    if (!target) return Promise.resolve();
    target.lists = order;
    return applyUnitOrder(units);
  }

  // moveUnitsTo relocates the selected units, preserving their relative order, so
  // the first lands at 1-based position posN among all units.
  function moveUnitsTo(keys: Set<string>, posN: number): Promise<void> {
    const units = computeUnits();
    const selected = units.filter((u) => keys.has(u.key));
    if (!selected.length) return Promise.resolve();
    const remaining = units.filter((u) => !keys.has(u.key));
    const idx = Math.max(0, Math.min(posN - 1, remaining.length));
    remaining.splice(idx, 0, ...selected);
    return applyUnitOrder(remaining);
  }

  async function linkSelected(): Promise<void> {
    const units = computeUnits();
    const selected = units.filter((u) => manageSelected.has(u.key));
    if (selected.length < 2 || selected.some((u) => u.kind !== "list")) return;
    const msg = byId("listsManageMessage");
    if (!xySync.requireOnline("Связывание списков доступно только онлайн.", msg)) return;
    const name = (prompt("Название списка списков:", "") || "").trim();
    if (!name) return;
    // Preserve board order (units are rank-sorted).
    const listIds = selected.sort((a, b) => units.indexOf(a) - units.indexOf(b)).flatMap((u) => u.lists.map((l) => l.id));
    try {
      await jpost(`/api/boards/${board.id}/list-groups`, { name_enc: await xyCrypto.encField(board.dk(), name), list_ids: listIds });
      manageSelected = new Set();
      await board.reload();
      renderManage();
    } catch (err) { msg.textContent = errMsg(err); }
  }

  async function renameGroup(gid: number): Promise<void> {
    const g = board.groupById(gid);
    const name = (prompt("Новое название группы:", g ? g.name : "") || "").trim();
    if (!name) return;
    const msg = byId("listsManageMessage");
    if (!xySync.requireOnline("Переименование доступно только онлайн.", msg)) return;
    try {
      await jpatch(`/api/list-groups/${gid}`, { name_enc: await xyCrypto.encField(board.dk(), name) });
      await board.reload();
      renderManage();
    } catch (err) { msg.textContent = errMsg(err); }
  }

  async function unlinkGroup(gid: number): Promise<void> {
    if (!confirm("Разъединить группу? Списки останутся, но нумерация снова станет раздельной.")) return;
    const msg = byId("listsManageMessage");
    if (!xySync.requireOnline("Разъединение доступно только онлайн.", msg)) return;
    try {
      await jdelete(`/api/list-groups/${gid}`);
      await board.reload();
      renderManage();
    } catch (err) { msg.textContent = errMsg(err); }
  }

  byId("listsLinkBtn").addEventListener("click", () => { void linkSelected(); });
  byId("listsMoveBtn").addEventListener("click", () => {
    const n = parseInt(byId<HTMLInputElement>("listsMovePos").value, 10);
    if (!(n >= 1)) { listsManageModal.message("Укажите позицию."); return; }
    void moveUnitsTo(new Set(manageSelected), n);
  });


  return {
    applyUnitOrder,
    panel: {
      id: "lists-manage", menu: "board", icon: "columns-3",
      label: "Управление списками",
      title: "Переупорядочить списки и связать их в группы (списки списков)",
      open: openListsManage,
    },
  };
}
