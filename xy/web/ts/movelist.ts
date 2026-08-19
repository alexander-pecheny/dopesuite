// movelist.ts — «Переместить список…»: move or copy a whole list. Within the
// board a move is a re-rank and a copy a duplicate; to another board it is a
// client-side re-encryption of the list title, every card and its labels and
// playings, mirroring the per-card move/copy in carddetail.ts. The destination
// board is chosen by its (decrypted) name and the insertion position among its
// lists is selectable.

import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { xyRank } from "./rank.js";
import { rankForSlot } from "./dragrank.js";
import { nowStamp } from "./carddetail.js";
import { modal } from "./modal.js";
import type { MoveCtx } from "./carddetail.js";
import type { Transfer } from "./transfer.js";
import type { Board, ListPanel } from "./panels.js";
import type { BoardList } from "./unlock.js";

const { fetchJSON, jpost, jput, jdelete, el, byId, errMsg } = xyApp;
const { keyBetween } = xyRank;

interface MoveBoardItem { id: number; name?: string; name_enc?: string | null; schema_version?: number }

export function createMoveListPanel(board: Board, transfer: Pick<Transfer, "loadMoveBoard" | "transferCard">): ListPanel {

  let listMoveSrc: BoardList | null = null;  // the list being moved/copied
  let listMoveCtx: MoveCtx | null = null;  // destination board ctx (from loadMoveBoard)

  const moveListModal = modal("moveList");

  function openMoveList(list: BoardList): void {
    listMoveSrc = list;
    moveListModal.open();
    void populateMoveListBoards();
  }

  // populateMoveListBoards fills the board <select> with decrypted board names
  // (current board first/default), then loads the chosen board's list positions.
  async function populateMoveListBoards(): Promise<void> {
    const sel = byId<HTMLSelectElement>("moveListBoard");
    sel.replaceChildren();
    let boards: MoveBoardItem[] = [];
    try { boards = (await fetchJSON("/api/boards")) as MoveBoardItem[]; } catch (_) {}
    if (!boards.some((b) => b.id === board.id)) boards.unshift({ id: board.id, name_enc: null });
    for (const b of boards) {
      let label = "доска #" + b.id;
      if (b.id === board.id) label = (board.state.name || label) + " (эта доска)";
      else if ((b.schema_version ?? 0) >= 2) label = b.name || label; // plaintext name, no key needed
      else {
        try { const cdk = await xyCrypto.loadCachedDK(b.id); if (cdk) label = await xyCrypto.decField(cdk, b.name_enc || ""); }
        catch (_) {}
      }
      sel.append(el("option", { value: b.id, text: label }));
    }
    sel.value = String(board.id);
    await onMoveListBoardChange();
  }

  // onMoveListBoardChange loads the destination board (prompting for its password
  // when it isn't unlocked — see loadMoveBoard→ensureDK) and rebuilds the position
  // <select> with one slot per existing list ("в конец" appends).
  async function onMoveListBoardChange(): Promise<void> {
    const posSel = byId<HTMLSelectElement>("moveListPos");
    const bid = Number(byId<HTMLSelectElement>("moveListBoard").value);
    posSel.replaceChildren(el("option", { value: "", text: "загрузка…" }));
    try { listMoveCtx = await transfer.loadMoveBoard(bid); }
    catch (err) {
      listMoveCtx = null;
      posSel.replaceChildren(el("option", { value: "", text: errMsg(err) }));
      return;
    }
    const ctx = listMoveCtx, src = listMoveSrc;
    const lists = ctx.lists.filter((l) => !(ctx.boardId === board.id && src && l.id === src.id));
    posSel.replaceChildren(el("option", { value: "end", text: "в конец" }));
    for (let i = 1; i <= lists.length; i++) posSel.append(el("option", { value: String(i), text: `позиция ${i}` }));
    posSel.value = "end";
  }

  async function doMoveListCopy(remove: boolean): Promise<void> {
    const src = listMoveSrc, ctx = listMoveCtx;
    if (!src || !ctx) return;
    // A cross-board copy re-encrypts every card, comment and attachment — seconds
    // during which the modal stays open; a second click used to start a second
    // copy and leave a duplicated list on the target board.
    const copyBtn = byId<HTMLButtonElement>("moveListCopyBtn");
    const moveBtn = byId<HTMLButtonElement>("moveListMoveBtn");
    if (copyBtn.disabled) return;
    copyBtn.disabled = moveBtn.disabled = true;
    try {
      await moveListCopyLocked(remove, src, ctx);
    } finally {
      copyBtn.disabled = moveBtn.disabled = false;
    }
  }

  async function moveListCopyLocked(remove: boolean, src: BoardList, ctx: MoveCtx): Promise<void> {
    const targetBid = ctx.boardId;
    const sameBoard = targetBid === board.id;
    const msg = byId("moveListMessage");
    const rank = rankForSlot(ctx.lists, byId<HTMLSelectElement>("moveListPos").value, sameBoard ? src.id : undefined);
    const srcCards = board.cardsOf(src.id);
    const type = src.type || "normal";

    // A grouped list must stay consecutive with its group, so reordering it on the
    // same board goes through «Управление списками» (which moves the whole group as
    // a unit). Copying it, or moving it to another board, is still fine.
    if (sameBoard && remove && src.groupId != null) {
      msg.textContent = "Список входит в группу — измените порядок через «Управление списками».";
      return;
    }

    // Same-board move is just a re-rank (no re-encryption needed).
    if (sameBoard && remove) {
      src.rank = rank;
      board.setStatus("saving");
      try {
        await board.verbs.patch("patchList", `/api/lists/${src.id}`, { rank });
        board.setStatus("saved"); board.render(); moveListModal.close();
      } catch (err) { board.setStatus("error"); msg.textContent = errMsg(err); void board.reload(); }
      return;
    }

    // Copying a list (it carries every card's comments/attachments) and any
    // cross-board op are online-only; only the intra-board move above works offline.
    if (!xySync.requireOnline("Копирование и перенос между досками доступны только онлайн.", msg)) return;
    msg.textContent = sameBoard ? "Копирование…" : "Перешифровка…";
    try {
      // The new list, then every card through the one transfer path — a copy
      // on this board, a re-encryption onto another — in order.
      const key = sameBoard ? board.dk() : ctx.dk;
      const lres = (await jpost(`/api/boards/${targetBid}/lists`, {
        title_enc: await xyCrypto.encField(key, src.title), rank, type,
      })) as { id: number };
      if (sameBoard) board.state.lists.push({ id: lres.id, type, rank, groupId: null, title: src.title });
      for (const c of srcCards) await transfer.transferCard(c, lres.id, ctx, false);
      if (!sameBoard && remove) {
        await jdelete(`/api/lists/${src.id}`);
        board.state.lists = board.state.lists.filter((l) => l.id !== src.id);
        board.state.cards = board.state.cards.filter((c) => c.listId !== src.id);
      }
      board.render();
      msg.textContent = remove ? "Перемещено." : "Скопировано.";
      setTimeout(moveListModal.close, 700);
    } catch (err) { msg.textContent = errMsg(err); }
  }

  byId("moveListBoard").addEventListener("change", () => { void onMoveListBoardChange(); });
  byId("moveListCopyBtn").addEventListener("click", () => { void doMoveListCopy(false); });
  byId("moveListMoveBtn").addEventListener("click", () => { void doMoveListCopy(true); });


  return {
    id: "move-list", menu: "list", icon: "arrow-left-right",
    label: "Переместить список…",
    open: (scope) => openMoveList(scope.list),
  };
}
