// boardmembers.ts — the board's members/sharing seam, lifted out of board.js as
// a first step in splitting that monolith into per-feature ES modules.
//
// Membership is plaintext server-side metadata (not board-encrypted), so the
// sharing modal works without the data key. Owners can add/remove editors;
// everyone else sees a read-only roster. The roster also feeds author names into
// the card timeline (member user_id → username), so it is cached on board load.
// The module writes members/memberNames/me onto the shared board `state` object
// board.js passes in, and owns the members overlay's DOM + listeners.
import { xyApp } from "./app.js";
import type { AuthMe } from "./app.js";
import { xySync } from "./sync.js";

const { fetchJSON, jpost, jdelete, el } = xyApp;

export interface BoardMember {
  user_id: number;
  username?: string | null;
  role: string;
}

// The slice of board.js's shared state this module reads (role) and writes
// (members / memberNames / me).
export interface MembersState {
  role?: string | null;
  members?: BoardMember[];
  memberNames?: Record<number, string>;
  me?: AuthMe | null;
}

// memberName / roleLabel are the pure display helpers (unit-tested).
export function memberName(m: BoardMember): string { return m.username || `#${m.user_id}`; }
export function roleLabel(role: string): string { return role === "owner" ? "владелец" : "редактор"; }

// createBoardMembers wires the members overlay against the shared board state and
// board id, and returns { load, open } for board.js to call.
export function createBoardMembers(state: MembersState, boardId: number | string): { load: () => Promise<void>; open: () => void } {
  async function fetchMembers(): Promise<BoardMember[]> {
    const members = (await fetchJSON(`/api/boards/${boardId}/members`)) as BoardMember[];
    state.members = members;
    state.memberNames = {};
    for (const m of members) state.memberNames[m.user_id] = memberName(m);
    return members;
  }

  async function load(): Promise<void> {
    if (!xySync.isOnline()) return;
    try { await fetchMembers(); } catch (_) {}
    if (!state.me) {
      try { state.me = (await fetchJSON(`/api/auth/me`)) as AuthMe; } catch (_) {}
    }
  }

  function open(): void {
    document.getElementById("membersMessage")!.textContent = "";
    document.getElementById("membersOverlay")!.hidden = false;
    render();
  }

  function close(): void { document.getElementById("membersOverlay")!.hidden = true; }

  async function render(): Promise<void> {
    const listNode = document.getElementById("membersList")!;
    const addForm = document.getElementById("addMemberForm")!;
    const msg = document.getElementById("membersMessage")!;
    listNode.replaceChildren();
    let members: BoardMember[];
    try {
      members = await fetchMembers();
    } catch (_) {
      msg.textContent = "Не удалось загрузить участников — нужно подключение к сети.";
      addForm.hidden = true;
      return;
    }
    const isOwner = state.role === "owner";
    addForm.hidden = !isOwner;
    for (const m of members) {
      const row = el("div", { class: "member-row" },
        el("span", { class: "member-name", text: memberName(m) }),
        el("span", { class: "member-role", text: roleLabel(m.role) }),
      );
      if (isOwner && m.role !== "owner") {
        row.append(el("button", {
          class: "attach-del member-del", type: "button", title: "Убрать из доски", text: "×",
          onclick: () => removeMember(m),
        }));
      }
      listNode.append(row);
    }
  }

  async function removeMember(m: BoardMember): Promise<void> {
    if (!confirm(`Убрать ${m.username || "участника"} из доски?`)) return;
    try {
      await jdelete(`/api/boards/${boardId}/members/${m.user_id}`);
      await render();
    } catch (e) {
      document.getElementById("membersMessage")!.textContent = e instanceof Error ? e.message : String(e);
    }
  }

  document.getElementById("addMemberForm")!.addEventListener("submit", async (e) => {
    e.preventDefault();
    const input = document.getElementById("addMemberName") as HTMLInputElement;
    const msg = document.getElementById("membersMessage")!;
    const name = input.value.trim();
    msg.textContent = "";
    if (!name) return;
    try {
      await jpost(`/api/boards/${boardId}/members`, { username: name });
      input.value = "";
      await render();
    } catch (err) {
      msg.textContent = err instanceof Error ? err.message : String(err);
    }
  });

  document.getElementById("membersClose")!.addEventListener("click", close);
  document.getElementById("membersOverlay")!.addEventListener("pointerdown", (e) => {
    if (e.target instanceof Element && e.target.id === "membersOverlay") close();
  });

  return { load, open };
}

export const xyBoardMembers = { createBoardMembers, memberName, roleLabel };
