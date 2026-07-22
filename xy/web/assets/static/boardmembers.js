// boardmembers.js — the board's members/sharing seam, lifted out of board.js as
// a first step in splitting that monolith into per-feature ES modules.
//
// Membership is plaintext server-side metadata (not board-encrypted), so the
// sharing modal works without the data key. Owners can add/remove editors;
// everyone else sees a read-only roster. The roster also feeds author names into
// the card timeline (member user_id → username), so it is cached on board load.
// The module writes members/memberNames/me onto the shared board `state` object
// board.js passes in, and owns the members overlay's DOM + listeners.
import { xyApp } from "./app.js";
import { xySync } from "./sync.js";

const { fetchJSON, jpost, jdelete, el } = xyApp;

// memberName / roleLabel are the pure display helpers (unit-tested).
export function memberName(m) { return m.username || `#${m.user_id}`; }
export function roleLabel(role) { return role === "owner" ? "владелец" : "редактор"; }

// createBoardMembers wires the members overlay against the shared board state and
// board id, and returns { load, open } for board.js to call.
export function createBoardMembers(state, boardId) {
  async function fetchMembers() {
    const members = await fetchJSON(`/api/boards/${boardId}/members`);
    state.members = members;
    state.memberNames = {};
    for (const m of members) state.memberNames[m.user_id] = memberName(m);
    return members;
  }

  async function load() {
    if (!xySync.isOnline()) return;
    try { await fetchMembers(); } catch (_) {}
    if (!state.me) {
      try { state.me = await fetchJSON(`/api/auth/me`); } catch (_) {}
    }
  }

  function open() {
    document.getElementById("membersMessage").textContent = "";
    document.getElementById("membersOverlay").hidden = false;
    render();
  }

  function close() { document.getElementById("membersOverlay").hidden = true; }

  async function render() {
    const listNode = document.getElementById("membersList");
    const addForm = document.getElementById("addMemberForm");
    const msg = document.getElementById("membersMessage");
    listNode.replaceChildren();
    let members;
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

  async function removeMember(m) {
    if (!confirm(`Убрать ${m.username || "участника"} из доски?`)) return;
    try {
      await jdelete(`/api/boards/${boardId}/members/${m.user_id}`);
      await render();
    } catch (e) {
      document.getElementById("membersMessage").textContent = e.message;
    }
  }

  document.getElementById("addMemberForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const input = document.getElementById("addMemberName");
    const msg = document.getElementById("membersMessage");
    const name = input.value.trim();
    msg.textContent = "";
    if (!name) return;
    try {
      await jpost(`/api/boards/${boardId}/members`, { username: name });
      input.value = "";
      await render();
    } catch (err) {
      msg.textContent = err.message;
    }
  });

  document.getElementById("membersClose").addEventListener("click", close);
  document.getElementById("membersOverlay").addEventListener("pointerdown", (e) => {
    if (e.target.id === "membersOverlay") close();
  });

  return { load, open };
}

export const xyBoardMembers = { createBoardMembers, memberName, roleLabel };

if (typeof window !== "undefined") window.xyBoardMembers = xyBoardMembers;
