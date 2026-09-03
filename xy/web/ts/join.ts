// join.ts — the page an invite link opens (ADR-0017). It says which board this
// is and what the link can still do for you, and joining is one button. A link
// carries membership only: what you get here is the right to fetch the board's
// ciphertext, and the passphrase still has to reach you from a person.
import { xyApp } from "./app.js";
import S from "./i18nstrings.js";

const { jpost, el, byId, errMsg } = xyApp;

// What the link does for the caller: the link's own state, or their history
// with it. Kept as a local union rather than imported from boardinvites.ts —
// this page has no business pulling the board's module graph in.
export type PeekState =
  | "active" | "revoked" | "expired" | "exhausted"
  | "member" | "pending" | "declined" | "spent";

export interface InvitePeek {
  board_id: number;
  board_name: string;
  state: PeekState;
  requires_approval: boolean;
}

// Joining ends in one of two places: in, or in the queue.
export interface JoinResult {
  board_id: number;
  state: "member" | "pending";
}

// joinView is the whole page as a decision on the peek: a heading, a line of
// explanation and at most one action. Pure, so the states are testable without
// a DOM (a link is dead in five different ways and each says something else).
export interface JoinView {
  heading: string;
  note: string;
  action: "join" | "open" | "none";
}

export function joinView(peek: InvitePeek): JoinView {
  const board = peek.board_name ? S.invite.page.boardWrapper(peek.board_name) : S.invite.page.boardFallback();
  switch (peek.state) {
    case "active":
      return peek.requires_approval
        ? { heading: `Заявка в ${board}`, note: "Владелец доски рассмотрит заявку — доступ появится после одобрения.", action: "join" }
        : { heading: `Приглашение в ${board}`, note: "Пароль доски придётся узнать у того, кто вас позвал: по ссылке он не передаётся.", action: "join" };
    case "member":
      return { heading: `Вы уже в ${board}`, note: "", action: "open" };
    case "pending":
      return { heading: "Заявка отправлена", note: "Ждём, пока владелец доски её рассмотрит.", action: "none" };
    case "declined":
      return { heading: "Заявка отклонена", note: "По этой ссылке войти больше нельзя — попросите новую.", action: "none" };
    case "revoked":
      return { heading: "Ссылка отозвана", note: "Попросите у владельца доски новую.", action: "none" };
    case "expired":
      return { heading: "Срок ссылки истёк", note: "Попросите у владельца доски новую.", action: "none" };
    case "exhausted":
      return { heading: "Ссылка исчерпана", note: "По ней уже прошли все, кого она пускала.", action: "none" };
    case "spent":
      return { heading: "По этой ссылке вы уже проходили", note: "Попросите у владельца доски новую.", action: "none" };
  }
  return { heading: "Ссылка не работает", note: "", action: "none" };
}

// The code is the last path segment: /join/<code>.
export function codeFromPath(path: string): string {
  return decodeURIComponent(path.replace(/\/+$/, "").split("/").pop() || "");
}

async function main(): Promise<void> {
  const code = codeFromPath(location.pathname);
  const body = byId("joinBody");
  const msg = byId("joinMessage");
  const res = await fetch(`/api/board-invites/code/${encodeURIComponent(code)}`, { credentials: "same-origin" });
  // Logged out is the common case for a link pasted into a messenger: go and log
  // in, and come back HERE rather than to the board list.
  if (res.status === 401) {
    location.replace(`/login?next=${encodeURIComponent(`/join/${code}`)}`);
    return;
  }
  if (!res.ok) {
    body.replaceChildren(el("h2", { text: "Ссылка не найдена" }));
    msg.textContent = (await res.text()).trim();
    return;
  }
  render((await res.json()) as InvitePeek);

  function render(p: InvitePeek): void {
    const view = joinView(p);
    const nodes: HTMLElement[] = [el("h2", { text: view.heading })];
    if (view.note) nodes.push(el("p", { class: "hint", text: view.note }));
    if (view.action === "open") {
      nodes.push(el("a", { class: "btn", href: `/board/${p.board_id}`, text: "Открыть доску" }));
    } else if (view.action === "join") {
      const btn = el("button", { class: "btn", type: "button", text: p.requires_approval ? "Подать заявку" : "Присоединиться" });
      btn.addEventListener("click", async () => {
        btn.setAttribute("disabled", "");
        msg.textContent = "";
        try {
          const res = (await jpost(`/api/board-invites/code/${encodeURIComponent(code)}/join`, {})) as JoinResult;
          if (res.state === "member") { location.replace(`/board/${res.board_id}`); return; }
          render({ ...p, state: res.state });
        } catch (err) {
          btn.removeAttribute("disabled");
          msg.textContent = errMsg(err);
        }
      });
      nodes.push(btn);
    }
    body.replaceChildren(...nodes);
  }
}

void main();
