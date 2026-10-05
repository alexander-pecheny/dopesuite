// addlist.ts — the add-list entry in a list's ⋯ menu: a new list placed right
// after (or before) this one, so a board with many lists needs no trip to the
// add-list column at the far end and a drag back. createList is the one create
// step, shared with that column.

import S from "./i18nstrings.js";
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xyRank } from "./rank.js";
import { byRank } from "./dragrank.js";
import { unitsOf } from "./listsmanage.js";
import { modal } from "./modal.js";
import type { Board, ListPanel } from "./panels.js";
import type { BoardList } from "./unlock.js";

const { byId, errMsg } = xyApp;
const { keyBetween } = xyRank;

// createList encrypts the title, creates the list (offline-capable via the sync
// outbox) and adds it to the board's state. The caller renders.
export async function createList(board: Board, title: string, type: string, rank: string): Promise<void> {
  const titleEnc = await xyCrypto.encField(board.dk(), title);
  const res = await board.verbs.create("createList", `/api/boards/${board.id}/lists`, { title_enc: titleEnc, rank, type });
  board.state.lists.push({ id: res.id as number, type, rank, groupId: null, title });
}

// rankNextTo is the rank of a new ungrouped list beside `anchor`. A list in a
// group is passed by the whole group, before or after it, because a group's
// members stay consecutive. Null when the anchor is gone.
function rankNextTo(lists: readonly BoardList[], anchor: BoardList, before: boolean): string | null {
  const units = unitsOf([...lists].sort(byRank));
  const i = units.findIndex((u) => u.lists.some((l) => l.id === anchor.id));
  if (i < 0) return null;
  const lo = before ? units[i - 1] : units[i];
  const hi = before ? units[i] : units[i + 1];
  const prev = lo ? lo.lists[lo.lists.length - 1].rank : null;
  const next = hi ? hi.lists[0].rank : null;
  // Two lists can share a rank (two clients appending at once), and keyBetween
  // refuses an empty gap; then the new list goes right after `prev`.
  try { return keyBetween(prev, next); } catch { return keyBetween(prev, null); }
}

export function createAddListPanel(board: Board): ListPanel {
  const addListModal = modal("addList");
  const form = byId<HTMLFormElement>("addListForm");
  const nameInput = byId<HTMLInputElement>("addListName");
  const typeSelect = byId<HTMLSelectElement>("addListType");
  let anchor: BoardList | null = null;

  function open(list: BoardList): void {
    anchor = list;
    nameInput.value = "";
    typeSelect.value = list.type === "si" ? "si" : "normal";
    byId<HTMLInputElement>("addListAfter").checked = true;
    addListModal.open({ onClose: () => { anchor = null; } });
    nameInput.focus();
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const list = anchor;
    const title = nameInput.value.trim();
    if (!list || !title || form.inert) return;
    const rank = rankNextTo(board.state.lists, list, byId<HTMLInputElement>("addListBefore").checked);
    if (rank == null) { addListModal.close(); return; }
    form.inert = true; // a second Enter would make a second list
    board.setStatus("saving");
    try {
      await createList(board, title, typeSelect.value, rank);
      board.setStatus("saved");
      addListModal.close();
      board.render();
    } catch (err) {
      board.setStatus("error");
      addListModal.message(S.board.list.addFailed(errMsg(err)));
    } finally { form.inert = false; }
  });

  return { id: "add-list", menu: "list", icon: "list-plus", label: S.board.list.addLabel(), open: (s) => open(s.list) };
}
