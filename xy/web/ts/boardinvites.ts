// boardinvites.ts — invite links inside the «Участники» modal (ADR-0017).
// Owner-only: mint a link, watch it get used, revoke or delete it, and decide
// the Join Requests a link with approval collects. A link grants membership and
// never the key, so nothing here touches the data key or the passphrase.
import { xyApp } from "./app.js";
import { xySync } from "./sync.js";

const { fetchJSON, jpost, jdelete, el, errMsg } = xyApp;

// One person's passage through a link: who, and when they joined or asked.
export interface InvitePerson {
  user_id: number;
  username: string;
  at: string;
}

// Why a link does or does not work. It is the server's word — the client shows
// it rather than recomputing it from the counts, so the two cannot disagree.
export type InviteState = "active" | "revoked" | "expired" | "exhausted";

export interface BoardInvite {
  id: number;
  code: string;
  label: string;
  created_at: string;
  expires_at?: string;
  max_uses: number | null;
  used: number;
  left: number | null;
  requires_approval: boolean;
  state: InviteState;
  joined: InvitePerson[];
  pending: InvitePerson[];
}

// ---- the link row's wording (pure, unit-tested) ----

export function inviteStateLabel(state: string): string {
  switch (state) {
    case "active": return "активна";
    case "revoked": return "отозвана";
    case "expired": return "просрочена";
    case "exhausted": return "исчерпана";
  }
  return state;
}

// inviteUsage says how much of the link is spent. An uncapped link has nothing
// left to count down, so it only reports what it has done.
export function inviteUsage(inv: Pick<BoardInvite, "used" | "left">): string {
  const used = `использований: ${inv.used}`;
  return inv.left === null ? used : `${used}, осталось: ${inv.left}`;
}

// inviteTimeLeft is the coarse remainder — an editor deciding whether to resend
// a link cares about "3 дн", never about the minute. "" means the link has no
// expiry at all, which is not the same as an expiry that has passed.
export function inviteTimeLeft(expiresAt: string | undefined, now: number): string {
  if (!expiresAt) return "";
  const ms = new Date(expiresAt).getTime() - now;
  if (isNaN(ms)) return "";
  if (ms <= 0) return "срок истёк";
  const hours = Math.floor(ms / 3600000);
  if (hours >= 24) return `осталось ${Math.floor(hours / 24)} дн`;
  if (hours >= 1) return `осталось ${hours} ч`;
  return "осталось меньше часа";
}

export function inviteUrl(origin: string, code: string): string {
  return `${origin}/join/${code}`;
}

export function personName(p: InvitePerson): string {
  return p.username || `#${p.user_id}`;
}

export interface InvitesDeps {
  boardId: number | string;
  isOwner(): boolean;
  message(): HTMLElement;
  // A decision changes the roster too, so the modal redraws whole rather than
  // this module reaching into the member list.
  onChange(): void;
}

export function createBoardInvites(deps: InvitesDeps) {
  let invites: BoardInvite[] = [];

  function pendingCount(): number {
    return invites.reduce((n, i) => n + i.pending.length, 0);
  }

  // load is called with the roster as well as on open, so the ☰ can carry the
  // waiting count before anyone opens the modal.
  async function load(): Promise<void> {
    if (!deps.isOwner() || !xySync.isOnline()) { invites = []; return; }
    try { invites = (await fetchJSON(`/api/boards/${deps.boardId}/invites`)) as BoardInvite[]; } catch (_) { invites = []; }
  }

  // act runs one owner action and redraws, or says why it could not.
  async function act(fn: () => Promise<unknown>): Promise<void> {
    deps.message().textContent = "";
    try { await fn(); deps.onChange(); } catch (err) { deps.message().textContent = errMsg(err); }
  }

  function render(): void {
    renderJoinRequests();
    renderInvites();
  }

  // The waiting queue is drawn across every link: the owner decides about a
  // person, not about which link they came through.
  function renderJoinRequests(): void {
    const section = document.getElementById("joinRequestsSection")!;
    const box = document.getElementById("joinRequests")!;
    const waiting = invites.flatMap((inv) => inv.pending.map((p) => ({ inv, p })));
    section.hidden = waiting.length === 0;
    box.replaceChildren(...waiting.map(({ inv, p }) => el("div", { class: "member-row" },
      el("span", { class: "member-name", text: personName(p) }),
      el("span", { class: "member-role", text: inv.label || inv.code }),
      el("button", {
        class: "btn btn-ghost btn-small", type: "button", text: "Принять",
        onclick: () => { void act(() => jpost(`/api/boards/${deps.boardId}/join-requests/${p.user_id}`, { decision: "approve" })); },
      }),
      el("button", {
        class: "btn btn-ghost btn-small", type: "button", text: "Отклонить",
        onclick: () => { void act(() => jpost(`/api/boards/${deps.boardId}/join-requests/${p.user_id}`, { decision: "decline" })); },
      }),
    )));
  }

  function renderInvites(): void {
    const section = document.getElementById("invitesSection")!;
    const box = document.getElementById("inviteList")!;
    section.hidden = !deps.isOwner();
    document.getElementById("newInviteBtn")!.hidden = !deps.isOwner();
    const now = Date.now();
    box.replaceChildren(...invites.map((inv) => {
      const url = inviteUrl(location.origin, inv.code);
      const facts = [inviteStateLabel(inv.state), inviteUsage(inv), inviteTimeLeft(inv.expires_at, now)].filter(Boolean);
      if (inv.requires_approval) facts.push("с одобрением");
      const actions = el("div", { class: "invite-actions" },
        el("button", { class: "btn btn-ghost btn-small", type: "button", text: "Копировать", onclick: () => { void copyLink(url); } }),
      );
      if (inv.state !== "revoked") {
        actions.append(el("button", {
          class: "btn btn-ghost btn-small", type: "button", text: "Отозвать",
          onclick: () => { void act(() => jpost(`/api/board-invites/${inv.id}/revoke`, {})); },
        }));
      }
      actions.append(el("button", {
        class: "btn btn-ghost btn-small", type: "button", text: "Удалить",
        onclick: () => {
          if (!confirm("Удалить ссылку вместе с историей входов по ней?")) return;
          void act(() => jdelete(`/api/board-invites/${inv.id}`));
        },
      }));
      return el("div", { class: "invite-row" },
        el("div", { class: "invite-head" },
          el("span", { class: "invite-label", text: inv.label || "Ссылка" }),
          el("span", { class: "invite-facts", text: facts.join(" · ") }),
        ),
        el("div", { class: "invite-link", text: url }),
        actions,
        inv.joined.length ? el("div", { class: "invite-joined", text: "вошли: " + inv.joined.map(personName).join(", ") }) : null,
      );
    }));
  }

  // A clipboard the browser refuses still has to leave the link somewhere the
  // user can reach it, so the failure path prints it.
  async function copyLink(url: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(url);
      deps.message().textContent = "Ссылка скопирована.";
    } catch (_) { deps.message().textContent = url; }
  }

  // The mint form: two chip rows and a checkbox, collapsed behind «+ Создать
  // ссылку» so adding a member by username stays one row.
  let ttlHours = 0;
  let maxUses = 0;

  function chips(values: Array<[string, number]>, get: () => number, set: (v: number) => void): HTMLElement {
    const seg = el("div", { class: "seg" });
    const paint = (): void => {
      for (const [i, btn] of [...seg.children].entries()) btn.classList.toggle("active", values[i][1] === get());
    };
    for (const [text, value] of values) {
      const btn = el("button", { class: "seg-btn", type: "button", text });
      btn.addEventListener("click", () => { set(value); paint(); });
      seg.append(btn);
    }
    paint();
    return seg;
  }

  function buildForm(): void {
    const box = document.getElementById("inviteForm")!;
    const label = el("input", { class: "input", type: "text", placeholder: "Название, например «тестерам»", maxlength: "100" }) as HTMLInputElement;
    const approval = el("input", { type: "checkbox" }) as HTMLInputElement;
    const create = el("button", { class: "btn", type: "button", text: "Создать" }) as HTMLButtonElement;
    create.addEventListener("click", () => {
      create.disabled = true;
      void act(async () => {
        await jpost(`/api/boards/${deps.boardId}/invites`, {
          label: label.value, ttl_hours: ttlHours, max_uses: maxUses, requires_approval: approval.checked,
        });
        label.value = "";
        box.hidden = true;
      }).finally(() => { create.disabled = false; });
    });
    box.replaceChildren(
      label,
      el("div", { class: "invite-field" }, el("span", { class: "invite-field-label", text: "Срок" }),
        chips([["час", 1], ["сутки", 24], ["неделя", 168], ["без срока", 0]], () => ttlHours, (v) => { ttlHours = v; })),
      el("div", { class: "invite-field" }, el("span", { class: "invite-field-label", text: "Использований" }),
        chips([["1", 1], ["10", 10], ["100", 100], ["без ограничения", 0]], () => maxUses, (v) => { maxUses = v; })),
      el("label", { class: "invite-field" }, approval, el("span", { text: "С одобрением владельца" })),
      create,
    );
    box.hidden = true;
  }

  buildForm();
  document.getElementById("newInviteBtn")!.addEventListener("click", () => {
    const box = document.getElementById("inviteForm")!;
    box.hidden = !box.hidden;
  });

  return { load, render, pendingCount };
}

export const xyBoardInvites = { createBoardInvites, inviteStateLabel, inviteUsage, inviteTimeLeft, inviteUrl, personName };
