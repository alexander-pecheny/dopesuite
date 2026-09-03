// bell.ts — the 🔔: the badge (blue for an unread change by someone else, red
// when any of it mentions me) and the panel of recent other-authored activity,
// newest first, each row wording the event the way the Timeline does and opening
// its card. Read tracking is online-only best-effort, never through the outbox.
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { decodeCommentPayload, eventAuthor, eventVerb } from "./timeline.js";
import type { Board } from "./panels.js";
import type { BoardCard } from "./unlock.js";
import type { DataKey } from "./crypto.js";
import S from "./i18nstrings_ru_gen.js";

const { fetchJSON, jpost, el, deriveTitle } = xyApp;

export interface BellUI {
  toggle: HTMLElement;
  badge: HTMLElement;
}

export interface BellDeps {
  mustDK(): DataKey;
  cardTitle(card: BoardCard): string;
  openCard(card: BoardCard): Promise<void>;
  highlightComment(eventId: number): Promise<void>;
}

export interface Bell {
  renderBadge(): void;
  close(): void;
}

export function createBell(board: Board, ui: BellUI, deps: BellDeps): Bell {
  // renderNotifBadge shows the 🔔 badge iff any card has an unread bucket — red
  // when any of it mentions me.
  function renderNotifBadge(): void {
    const flags = Object.values(board.state.unread);
    ui.badge.hidden = !flags.some((u) => u.content || u.comments);
    ui.badge.classList.toggle("unread-dot-mention", flags.some((u) => u.mentions));
  }

  interface ActivityEvent {
    id: number;
    card_id: number;
    type: string;
    created_at: string;
    unread?: boolean;
    mention?: boolean;
    mention_reply?: boolean;
    reply_to_id?: number | null;
    payload_enc?: string;
    author_user_id?: number | null;
  }

  let notifPanelEl: HTMLElement | null = null;

  function closeNotifPanel(): void {
    if (!notifPanelEl) return;
    notifPanelEl.remove();
    notifPanelEl = null;
    ui.toggle.setAttribute("aria-expanded", "false");
    document.removeEventListener("pointerdown", onNotifOutside, true);
    document.removeEventListener("keydown", onNotifKey, true);
  }
  function onNotifOutside(e: PointerEvent): void {
    if (notifPanelEl && e.target instanceof Node && !notifPanelEl.contains(e.target) && e.target !== ui.toggle) closeNotifPanel();
  }
  // Transient popups (this panel, the ⋯ menu, the label picker) claim Escape in
  // the CAPTURE phase and stop it there. They are not on the overlay stack — no
  // history entry, nothing to go back to — but they are the innermost dismissible
  // thing on screen, so Escape must close them without also closing the card
  // underneath. Capture is what puts them ahead of the stack's own listener.
  function onNotifKey(e: KeyboardEvent): void {
    if (e.key !== "Escape") return;
    e.stopImmediatePropagation();
    closeNotifPanel();
  }

  async function openNotifPanel(): Promise<void> {
    if (notifPanelEl) { closeNotifPanel(); return; }
    const panel = el("div", { class: "popover notif-panel" });
    const head = el("div", { class: "notif-panel-head" },
      el("span", { text: S.chrome.bell.title() }),
      el("button", {
        class: "btn btn-small", type: "button", text: S.chrome.bell.readAll(),
        onclick: async () => {
          try { await jpost(`/api/boards/${board.id}/read-all`, {}); } catch (_) { return; }
          board.state.unread = {};
          board.render();
          renderNotifBadge();
          closeNotifPanel();
        },
      }));
    panel.append(head);
    const body = el("div", { class: "notif-panel-body" }, el("div", { class: "notif-empty", text: S.chrome.bell.loading() }));
    panel.append(body);
    ui.toggle.setAttribute("aria-expanded", "true");
    ui.toggle.parentElement?.append(panel);
    notifPanelEl = panel;
    document.addEventListener("pointerdown", onNotifOutside, true);
    document.addEventListener("keydown", onNotifKey, true);

    let events: ActivityEvent[] = [];
    try { events = (await fetchJSON(`/api/boards/${board.id}/activity`)) as ActivityEvent[]; } catch (_) {}
    if (notifPanelEl !== panel) return; // closed while loading
    body.replaceChildren();
    if (!events.length) { body.append(el("div", { class: "notif-empty", text: S.chrome.bell.empty() })); return; }
    for (const ev of events) {
      const card = board.state.cards.find((c) => c.id === ev.card_id);
      if (!card) continue; // card deleted/moved away since the event was recorded
      const row = el("button", { class: "notif-row", type: "button" });
      if (ev.unread) row.append(el("span", { class: "unread-dot" + (ev.mention ? " unread-dot-mention" : "") }));
      const verb = ev.mention ? (ev.mention_reply ? S.chrome.bell.mentionReply() : S.chrome.bell.mention()) : eventVerb(ev.type);
      const when = new Date(ev.created_at).toLocaleString("ru-RU");
      const bodyWrap = el("div", { class: "notif-row-body" },
        el("div", { class: "notif-row-meta", text: `${eventAuthor(ev, board.state.me, board.state.memberNames)} ${verb} · ${deps.cardTitle(card)} · ${when}` }));
      if (ev.type === "comment" || ev.type === "reaction") {
        let preview = "";
        try { preview = await xyCrypto.decField(deps.mustDK(), ev.payload_enc || ""); } catch (_) {}
        if (ev.type === "comment") preview = decodeCommentPayload(preview).text;
        bodyWrap.append(el("div", { class: "notif-row-preview", text: deriveTitle(preview, 120) }));
      }
      row.append(bodyWrap);
      row.addEventListener("click", () => {
        closeNotifPanel();
        void deps.openCard(card).then(() => { if (ev.type === "comment") void deps.highlightComment(ev.id); });
      });
      body.append(row);
    }
  }

  ui.toggle.addEventListener("click", () => { if (notifPanelEl) closeNotifPanel(); else void openNotifPanel(); });

  return { renderBadge: renderNotifBadge, close: closeNotifPanel };
}
