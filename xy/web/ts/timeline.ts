// timeline.ts — the card timeline (лента), lifted out of board.js into a typed
// create(deps) factory: event rendering (comments, desc_edit diffs, label +
// attachment events), the краткий/подробный diff preference, the expanded
// full-screen лента, one-level reply threads, comment edit/delete/выписка and
// the выписки overlay. The board injects what it owns (live state, DK, the
// outbox `post` verb, popupMenu, plural, attachment access); the card-detail
// module is reached through the `card` seam (open-card id + comment-link copy),
// which the orchestrator wires back to the carddetail factory's API.
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { xyDiff } from "./diff.js";
import type { AuthMe } from "./app.js";
import type { DataKey } from "./crypto.js";
import type { DiffOp } from "./diff.js";
import type { OpBody, TimelineEvent } from "./store.js";

const { fetchJSON, jpatch, jdelete, el } = xyApp;

// A timeline event as this module reads it: the synced/pending DTO plus the
// comment-specific flags the server adds and the client-recomputed reply_count.
export interface CardEvent extends TimelineEvent {
  deleted?: boolean;
  edited_at?: string | null;
  is_excerpt?: boolean;
  reply_count?: number;
}

// The slice of an attachment the выписки overlay needs (board's attachment
// DTOs carry more; structural, so they pass through unchanged).
export interface AttachmentLike {
  id: number;
  mime?: string | null;
  name?: string | null;
  is_excerpt?: boolean;
  [key: string]: unknown;
}

// One popupMenu item (board.js's popupMenu contract): `checked` makes it a
// checkbox row.
export interface MenuItem {
  label: string;
  onClick: () => void;
  checked?: boolean;
  icon?: Node;
}

// The slice of the board's live state the timeline reads: cards (for the
// «карточка создана» line) and the members roster (author names).
export interface TimelineState {
  cards: Array<{ id: number; createdAt: string | null }>;
  me?: AuthMe | null;
  memberNames?: Record<number, string>;
}

export interface TimelineDeps {
  getState(): TimelineState;
  getDK(): DataKey | null;
  // The board's outbox `post` verb (see board.js's mutate wrappers) — comments
  // are offline-capable, unlike the edit/delete/выписка mutations below.
  post(kind: string, path: string, body: OpBody): Promise<unknown>;
  popupMenu(anchor: HTMLElement, items: MenuItem[]): void;
  plural(n: number, one: string, few: string, many: string): string;
  card: {
    openCardId(): number | null;
    copyCommentLink(eventId: number): void;
  };
  attachments: {
    url(att: AttachmentLike): Promise<string>;
    download(att: AttachmentLike, name: string): Promise<void>;
  };
}

export interface Timeline {
  load(cardId: number): Promise<void>;
  events(): CardEvent[];
  // The board's loadAttachments hands over the fresh attachment list; the
  // timeline keeps the выписка-flagged ones and refreshes the counter.
  setAttachments(atts: AttachmentLike[]): void;
}

// ---- pure decision helpers (exported for tests) ----

// eventAuthor resolves a timeline event's author to a display name. Pending
// (offline, un-synced) events carry no author_user_id yet — they're authored by
// the current user, so fall back to "me".
export function eventAuthor(
  ev: { author_user_id?: number | null },
  me: AuthMe | null | undefined,
  memberNames: Record<number, string> | undefined,
): string {
  let uid = ev.author_user_id;
  if (uid == null && me) uid = me.user_id;
  if (uid == null) return "";
  const names = memberNames || {};
  if (names[uid]) return names[uid];
  if (me && me.user_id === uid && me.username) return me.username;
  return `#${uid}`;
}

// replyCountsOf recounts replies over the merged (synced + pending) event list.
// reply_count arrives from the server, which cannot see replies still sitting
// in the outbox — so «N ответов» would omit one composed offline. The client
// holds the card's WHOLE timeline (deleted replies already filtered out
// server-side), so recounting over the merged list is equivalent online and
// correct offline.
export function replyCountsOf(events: ReadonlyArray<{ id: number; reply_to_id?: number | null }>): Map<number, number> {
  const replies = new Map<number, number>();
  for (const e of events) {
    if (e.reply_to_id != null) replies.set(e.reply_to_id, (replies.get(e.reply_to_id) || 0) + 1);
  }
  return replies;
}

// orderThreadReplies picks a root's replies, oldest first. Synced replies order
// by id; un-synced ones are the newest of all but carry NEGATIVE temp ids, so a
// plain id sort would float them to the top. They go last, in the order they
// were queued (-1 queued before -2).
export function orderThreadReplies<T extends { id: number; reply_to_id?: number | null }>(
  events: readonly T[],
  rootId: number,
): T[] {
  const all = events.filter((e) => e.reply_to_id === rootId);
  return [
    ...all.filter((e) => e.id > 0).sort((a, b) => a.id - b.id),
    ...all.filter((e) => e.id <= 0).sort((a, b) => b.id - a.id),
  ];
}

// diffViewOf / feedOrderOf map the stored (localStorage) preference to its
// effective value; anything unrecognized falls back to the default.
export function diffViewOf(raw: string | null): "full" | "brief" {
  return raw === "full" ? "full" : "brief";
}
export function feedOrderOf(raw: string | null): "old" | "new" {
  return raw === "old" ? "old" : "new";
}

// orderFeedEvents: events are oldest→newest (by id), so "сначала новое" is the
// reverse.
export function orderFeedEvents<T>(events: readonly T[], order: "old" | "new"): T[] {
  return order === "old" ? [...events] : [...events].reverse();
}

// excerptComments picks the comments flagged as выписка.
export function excerptComments(events: ReadonlyArray<CardEvent>): CardEvent[] {
  return events.filter((e) => e.type === "comment" && !!e.is_excerpt);
}

// fullDiffSides is renderFullDiff's decision part: fold the diff ops into the
// two panes' runs — removed tokens (marked changed) live in "before", added in
// "after", equal runs in both.
export interface DiffSideRun { changed: boolean; text: string }
export function fullDiffSides(ops: readonly DiffOp[]): { before: DiffSideRun[]; after: DiffSideRun[] } {
  const before: DiffSideRun[] = [];
  const after: DiffSideRun[] = [];
  for (const op of ops) {
    if (op.type === "eq") {
      before.push({ changed: false, text: op.text });
      after.push({ changed: false, text: op.text });
    } else if (op.type === "del") {
      before.push({ changed: true, text: op.text });
    } else {
      after.push({ changed: true, text: op.text });
    }
  }
  return { before, after };
}

export function createTimeline(deps: TimelineDeps): Timeline {
  function byId<T extends HTMLElement = HTMLElement>(id: string): T {
    const node = document.getElementById(id);
    if (!node) throw new Error(`page is missing #${id}`);
    return node as T;
  }

  const state = deps.getState;

  // openCardEvents mirrors the open card's timeline (set by load) so the
  // expanded лента, threads, выписки and the card module's markCardRead can
  // reuse it without a re-fetch.
  let openCardEvents: CardEvent[] = [];
  let openCardExcerptAtts: AttachmentLike[] = [];
  let threadRootId: number | null = null;

  const feedOverlay = byId("feedOverlay");
  const threadOverlay = byId("threadOverlay");
  const excerptsOverlay = byId("excerptsOverlay");

  const author = (ev: { author_user_id?: number | null }): string => {
    const st = state();
    return eventAuthor(ev, st.me, st.memberNames);
  };

  // ---- timeline ----
  // load renders into a detached fragment and swaps it in once. Emptying
  // the лента first and appending as the decrypts resolved collapsed the card
  // overlay's scroll height mid-render, so the browser clamped the scroll position
  // and the view jumped up — every marked выписка threw the reader back to
  // «Выписок: N». The container must never be shorter than its content.
  async function load(cardId: number): Promise<void> {
    const tl = byId("timeline");
    // Refresh the cached server timeline when online, then merge any pending
    // (un-synced) events synthesized from the outbox so offline edits/comments show.
    if (xySync.isOnline()) {
      try {
        const ev = (await fetchJSON(`/api/cards/${cardId}/timeline`)) as CardEvent[];
        await xySync.cacheTimeline(cardId, ev);
      } catch (_) {}
    }
    let events: CardEvent[] = [];
    try { events = (await xySync.timelineFor(cardId)) as CardEvent[]; } catch (_) {}
    const replies = replyCountsOf(events);
    for (const e of events) e.reply_count = replies.get(e.id) || 0;
    if (cardId === deps.card.openCardId()) { openCardEvents = events; renderExcerptCount(); }
    // Newest first: events are oldest→newest (by id); show them reversed.
    const frag = document.createDocumentFragment();
    for (const ev of [...events].reverse()) {
      let payload = "";
      try {
        const dk = deps.getDK();
        if (dk) payload = await xyCrypto.decField(dk, ev.payload_enc || "");
      } catch (_) {}
      frag.append(renderEvent(ev, payload));
    }
    // Oldest goes last in the newest-first лента.
    const born = cardCreatedNode(cardId);
    if (born) frag.append(born);
    tl.replaceChildren(frag);
  }

  // cardCreatedNode is the «карточка создана» line closing the лента — the anchor
  // every later timestamp is read against. It is derived from cards.created_at
  // rather than from a timeline event, so it is there for every card ever made,
  // not just ones created after this shipped.
  function cardCreatedNode(cardId: number | null): HTMLElement | null {
    const card = state().cards.find((c) => c.id === cardId);
    if (!card || !card.createdAt) return null;
    return el("div", { class: "tl-event tl-born" },
      el("div", { class: "tl-meta", text: `карточка создана · ${new Date(card.createdAt).toLocaleString("ru-RU")}` }));
  }

  function renderEvent(ev: CardEvent, payload: string): HTMLElement {
    const when = new Date(ev.created_at).toLocaleString("ru-RU");
    const who = author(ev);
    const meta = (rest: string): string => (who ? `${who} · ${rest}` : rest);
    const wrap = el("div", { class: "tl-event tl-" + ev.type });
    if (ev.type === "comment") {
      // A tombstone: the text is gone from the server, but the comment is still
      // rendered because replies hang off it — losing the anchor would orphan them.
      if (ev.deleted) {
        wrap.classList.add("tl-deleted");
        wrap.id = "tlev-" + ev.id;
        const row = el("div", { class: "tl-meta" }, "комментарий удалён");
        if ((ev.reply_count || 0) > 0) row.append(threadButton(ev));
        wrap.append(row);
        return wrap;
      }
      const metaRow = el("div", { class: "tl-meta" }, meta(when + (ev.edited_at ? " · изменён" : "")));
      if (ev.is_excerpt) {
        wrap.classList.add("tl-excerpt");
        metaRow.append(el("span", { class: "tl-badge", text: "выписка" }));
      }
      // A reply keeps its place in the flat лента (it is part of the card's
      // history) but says what it answers, and links up to it. Added BEFORE
      // .tl-actions, which is margin-left:auto and would otherwise push this to
      // the far right of the row.
      if (ev.reply_to_id) {
        const rootId = ev.reply_to_id;
        const parent = (openCardEvents || []).find((e) => e.id === rootId);
        const parentWho = parent ? (parent.deleted ? "удалённый комментарий" : author(parent)) : "комментарий";
        metaRow.append(el("span", { class: "tl-sep", text: "·" }), el("button", {
          class: "tl-replyto", type: "button", title: "Открыть ветку",
          text: `↳ в ответ ${parentWho}`, onclick: () => { void openThread(rootId); },
        }));
      }
      // Synced comments have a stable event id → offer a copyable direct link, the
      // edit/delete/выписка menu, and an anchor target. Pending (offline) comments
      // have no id yet, so none of that can address them.
      if (ev.id) {
        wrap.id = "tlev-" + ev.id;
        // Right-anchored so both controls sit in the comment's top-right corner
        // rather than trailing a timestamp of unpredictable width.
        metaRow.append(el("div", { class: "tl-actions" },
          el("button", {
            class: "tl-link", type: "button", title: "Копировать ссылку на комментарий",
            text: "🔗", onclick: () => deps.card.copyCommentLink(ev.id),
          }),
          el("button", {
            class: "tl-menu", type: "button", title: "Действия с комментарием", "aria-haspopup": "true",
            text: "⋯", onclick: (e: Event) => commentMenu(e.currentTarget as HTMLElement, ev, payload),
          })));
      }
      wrap.append(metaRow, el("div", { class: "tl-comment", text: payload }));
      if ((ev.reply_count || 0) > 0) wrap.append(threadButton(ev));
    } else if (ev.type === "desc_edit") {
      let diff: { before?: string; after?: string; author?: string } = {};
      try { diff = JSON.parse(payload) as { before?: string; after?: string; author?: string }; } catch (_) {}
      const ops = xyDiff.diffTokens(diff.before || "", diff.after || "");
      // An imported edit (Trello history) names its author inside the payload —
      // they are not an xy user, so author_user_id has nobody to point at.
      const editor = diff.author ? `${diff.author} · ` : meta("");
      wrap.append(el("div", { class: "tl-meta", text: editor + "правка описания · " + when }),
        diffView() === "brief" ? renderBriefDiff(ops) : renderFullDiff(ops));
    } else {
      let info: { label?: string; file?: string } = {};
      try { info = JSON.parse(payload) as { label?: string; file?: string }; } catch (_) {}
      const verbs: Record<string, string> = {
        label_add: "добавлена метка", label_remove: "снята метка",
        attach_add: "вложение добавлено", attach_remove: "вложение удалено", attach_replace: "вложение заменено",
      };
      const verb = verbs[ev.type] || ev.type;
      const detail = info.label || info.file || "";
      wrap.append(el("div", { class: "tl-meta", text: meta(`${verb}${detail ? ": " + detail : ""} · ${when}`) }));
    }
    return wrap;
  }

  // ---- desc_edit rendering: краткий / подробный ----
  // A card's description is long and an edit usually touches a few words, so the
  // default (краткий) shows just those with a little context. подробный is the
  // original two-pane before/after, kept for when the whole text matters. The
  // choice is a per-reader display preference, so it lives in localStorage beside
  // the other display prefs rather than on the server.
  const DIFF_VIEW_KEY = "xy.diffView";
  function diffView(): "full" | "brief" {
    return diffViewOf(localStorage.getItem(DIFF_VIEW_KEY));
  }

  // renderFullDiff: two panes, changes highlighted within each — removed tokens
  // struck through in "before", added tokens highlighted in "after".
  function renderFullDiff(ops: DiffOp[]): HTMLElement {
    const sides = fullDiffSides(ops);
    const pane = (runs: DiffSideRun[], cls: string, tag: string): HTMLElement => {
      const box = el("div", { class: cls });
      for (const run of runs) {
        if (run.changed) box.append(el(tag, { class: "tl-chg", text: run.text }));
        else box.append(document.createTextNode(run.text));
      }
      return box;
    };
    return el("div", { class: "tl-diff" },
      pane(sides.before, "tl-before", "del"),
      pane(sides.after, "tl-after", "ins"));
  }

  // renderBriefDiff: one flowing line, old and new inline, the untouched bulk
  // replaced by … (xyDiff.briefOps decides what survives).
  function renderBriefDiff(ops: DiffOp[]): HTMLElement {
    const box = el("div", { class: "tl-brief" });
    for (const op of xyDiff.briefOps(ops)) {
      if (op.type === "eq") box.append(document.createTextNode(op.text));
      else if (op.type === "gap") box.append(el("span", { class: "tl-gap", text: " … " }));
      else if (op.type === "del") box.append(el("del", { class: "tl-chg", text: op.text }));
      else box.append(el("ins", { class: "tl-chg", text: op.text }));
    }
    // An edit that changed only whitespace leaves nothing visible to show.
    if (!(box.textContent || "").trim()) box.append(el("span", { class: "tl-gap", text: "без видимых изменений" }));
    return box;
  }

  // setDiffView keeps the two selects (card + expanded лента) in step and
  // re-renders whichever feeds are on screen.
  async function setDiffView(v: string): Promise<void> {
    localStorage.setItem(DIFF_VIEW_KEY, v === "full" ? "full" : "brief");
    for (const id of ["feedDiffView", "feedDiffViewFull"]) byId<HTMLSelectElement>(id).value = diffView();
    const oc = deps.card.openCardId();
    if (oc) await load(oc);
    if (!feedOverlay.hidden) await renderFeedGrid();
  }

  for (const id of ["feedDiffView", "feedDiffViewFull"]) {
    const sel = byId<HTMLSelectElement>(id);
    sel.value = diffView();
    sel.addEventListener("change", () => { void setDiffView(sel.value); });
  }

  // ---- expanded лента ----
  // The card panel gives the лента ~320px of height; on a long discussion that is
  // a keyhole. Развернуть re-renders the same events full-screen, flowed into
  // columns so as much as possible is readable at once.
  function closeFeed(): void { feedOverlay.hidden = true; }

  // Reading order in the expanded лента. The panel's feed is always newest-first
  // (you go there for what just happened); reading a whole discussion end to end
  // is the other job, and that one wants oldest-first.
  const FEED_ORDER_KEY = "xy.feedOrder";
  function feedOrder(): "old" | "new" {
    return feedOrderOf(localStorage.getItem(FEED_ORDER_KEY));
  }

  async function renderFeedGrid(): Promise<void> {
    const grid = byId("feedGrid");
    const frag = document.createDocumentFragment();
    // openCardEvents is oldest→newest (by id), so "сначала новое" is the reverse.
    const ordered = orderFeedEvents(openCardEvents || [], feedOrder());
    for (const ev of ordered) {
      let payload = "";
      if (!ev.deleted) {
        try {
          const dk = deps.getDK();
          if (dk) payload = await xyCrypto.decField(dk, ev.payload_enc || "");
        } catch (_) {}
      }
      const node = renderEvent(ev, payload);
      // The panel's лента already owns tlev-{id}; these are a SECOND rendering of
      // the same events, so they must not duplicate those ids — deep links and
      // highlightComment resolve by id and would land on whichever came first.
      node.removeAttribute("id");
      frag.append(node);
    }
    // whichever end of this ordering is the oldest
    const born = cardCreatedNode(deps.card.openCardId());
    if (born) { if (feedOrder() === "old") frag.prepend(born); else frag.append(born); }
    grid.replaceChildren(frag);
  }

  const feedOrderSel = byId<HTMLSelectElement>("feedOrder");
  feedOrderSel.value = feedOrder();
  feedOrderSel.addEventListener("change", async () => {
    localStorage.setItem(FEED_ORDER_KEY, feedOrderSel.value === "old" ? "old" : "new");
    if (!feedOverlay.hidden) await renderFeedGrid();
  });

  byId("feedExpand").addEventListener("click", async () => {
    feedOverlay.hidden = false;
    await renderFeedGrid();
  });
  byId("feedClose").addEventListener("click", closeFeed);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !feedOverlay.hidden) closeFeed(); });

  // ---- reply threads ----
  // Threads are one level deep and live in a modal: the лента stays flat and
  // newest-first (it is a history), while a thread reads oldest-first (it is a
  // conversation). Replies appear in BOTH — the лента never hides a comment.
  function threadButton(ev: CardEvent): HTMLElement {
    const n = ev.reply_count || 0;
    return el("button", {
      class: "tl-thread", type: "button",
      text: `💬 ${n} ${deps.plural(n, "ответ", "ответа", "ответов")}`,
      onclick: () => { void openThread(ev.id); },
    });
  }

  function closeThread(): void { threadOverlay.hidden = true; threadRootId = null; }

  // openThread renders the root comment and its replies, oldest first, from the
  // events already loaded for the open card — no extra round trip.
  async function openThread(rootId: number): Promise<void> {
    threadRootId = rootId;
    const events = openCardEvents || [];
    const root = events.find((e) => e.id === rootId);
    const replies = orderThreadReplies(events, rootId);
    const body = byId("threadBody");
    const frag = document.createDocumentFragment();
    for (const ev of [root, ...replies]) {
      if (!ev) continue;
      let text = "";
      if (!ev.deleted) {
        try {
          const dk = deps.getDK();
          if (dk) text = await xyCrypto.decField(dk, ev.payload_enc || "");
        } catch (_) {}
      }
      const node = el("div", { class: "thread-item" + (ev.id === rootId ? " thread-root" : "") },
        el("div", { class: "tl-meta" },
          ev.deleted ? "комментарий удалён"
            : `${author(ev)} · ${new Date(ev.created_at).toLocaleString("ru-RU")}${ev.edited_at ? " · изменён" : ""}`,
          ev.is_excerpt ? el("span", { class: "tl-badge", text: "выписка" }) : null),
        ev.deleted ? null : el("div", { class: "tl-comment", text }));
      frag.append(node);
    }
    body.replaceChildren(frag);
    byId("threadMessage").textContent = "";
    threadOverlay.hidden = false;
  }

  byId("threadForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const input = byId<HTMLInputElement>("threadInput");
    const text = input.value.trim();
    const oc = deps.card.openCardId();
    if (!text || !oc || !threadRootId) return;
    const msg = byId("threadMessage");
    try {
      const dk = mustDK();
      await deps.post("comment", `/api/cards/${oc}/comments`, {
        payload_enc: await xyCrypto.encField(dk, text), reply_to_id: threadRootId,
      });
      input.value = "";
      await load(oc);
      await openThread(threadRootId); // re-render the thread with the new reply
    } catch (err) { msg.textContent = err instanceof Error ? err.message : String(err); }
  });

  byId("threadClose").addEventListener("click", closeThread);
  threadOverlay.addEventListener("pointerdown", (e) => { if (e.target === threadOverlay) closeThread(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !threadOverlay.hidden) closeThread(); });

  function mustDK(): DataKey {
    const dk = deps.getDK();
    if (!dk) throw new Error("нет ключа доски");
    return dk;
  }

  // ---- comment edit / delete / выписка ----
  // Rewriting or removing a comment is the author's business; flagging one as a
  // выписка is curation any member may do (the server draws the same line in
  // handlePatchComment). All three are online-only, like attachment mutations: a
  // queued edit of a comment that has not itself synced yet is a temp-id knot the
  // outbox has no reason to learn.
  function commentMenu(anchor: HTMLElement, ev: CardEvent, payload: string): void {
    const st = state();
    const mine = !!(st.me && ev.author_user_id === st.me.user_id);
    // Replying opens the thread (with its composer) — for a comment with no
    // replies yet, that is just the comment plus an empty answer box.
    const items: MenuItem[] = [{ label: "↩️ Ответить", onClick: () => { void openThread(ev.reply_to_id || ev.id); } }];
    if (mine) {
      // The node is taken from the anchor, not looked up by id: the same comment
      // may also be rendered in the expanded лента, and the edit must open on the
      // copy whose ⋯ was actually clicked.
      items.push({ label: "✏️ Редактировать", onClick: () => startCommentEdit(ev, payload, anchor.closest<HTMLElement>(".tl-event")) });
      items.push({ label: "🗑 Удалить", onClick: () => deleteComment(ev) });
    }
    items.push({
      label: "Выписка", checked: !!ev.is_excerpt,
      onClick: () => { void commentAction(() => jpatch(`/api/comments/${ev.id}`, { is_excerpt: !ev.is_excerpt })); },
    });
    deps.popupMenu(anchor, items);
  }

  // commentAction runs one comment mutation and re-renders the лента (which also
  // refreshes the выписки counter), reporting failures in the card's message line.
  async function commentAction(fn: () => Promise<unknown>): Promise<void> {
    const msg = byId("cardMessage");
    if (!xySync.requireOnline("Правка комментариев доступна только онлайн.", msg)) return;
    try {
      await fn();
      msg.textContent = "";
      await refreshFeeds();
    } catch (err) { msg.textContent = err instanceof Error ? err.message : String(err); }
  }

  // refreshFeeds re-renders both places a comment can appear: the card panel's
  // лента and, when open, the expanded one.
  async function refreshFeeds(): Promise<void> {
    const oc = deps.card.openCardId();
    if (oc != null) await load(oc);
    if (!feedOverlay.hidden) await renderFeedGrid();
  }

  function deleteComment(ev: CardEvent): void {
    if (!confirm("Удалить комментарий?")) return;
    void commentAction(() => jdelete(`/api/comments/${ev.id}`));
  }

  // startCommentEdit swaps the comment's body for a textarea in place, so the
  // surrounding лента stays put while it is edited.
  function startCommentEdit(ev: CardEvent, payload: string, wrap: HTMLElement | null): void {
    if (!wrap || wrap.querySelector(".tl-edit")) return;
    const body = wrap.querySelector(".tl-comment");
    if (!body) return;
    const ta = el("textarea", { class: "card-desc comment-input tl-edit", spellcheck: "false" }) as HTMLTextAreaElement;
    ta.value = payload;
    const save = el("button", {
      class: "btn btn-small", type: "button", text: "Сохранить",
      onclick: async () => {
        const text = ta.value.trim();
        if (!text) return;
        await commentAction(async () => jpatch(`/api/comments/${ev.id}`, { payload_enc: await xyCrypto.encField(mustDK(), text) }));
      },
    });
    const cancel = el("button", {
      class: "btn btn-small btn-ghost", type: "button", text: "Отмена",
      onclick: () => { void refreshFeeds(); },
    });
    body.replaceWith(el("div", { class: "tl-editbox" }, ta, el("div", { class: "tl-editrow" }, save, cancel)));
    ta.focus();
  }

  byId("commentForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const input = byId<HTMLInputElement>("commentInput");
    const text = input.value.trim();
    const oc = deps.card.openCardId();
    if (!text || !oc) return;
    try {
      await deps.post("comment", `/api/cards/${oc}/comments`, { payload_enc: await xyCrypto.encField(mustDK(), text) });
      input.value = "";
      await load(oc);
    } catch (err) { byId("cardMessage").textContent = err instanceof Error ? err.message : String(err); }
  });

  // ---- выписки ----
  // A выписка is an excerpt from a source — a comment or an attachment flagged as
  // such — so the sources behind a question can be re-read mid-edit without
  // scrolling the whole лента or opening attachments one browser tab at a time.
  // The flag is a plaintext column server-side (migrateV14); the content is not.
  function renderExcerptCount(): void {
    const n = excerptComments(openCardEvents || []).length + openCardExcerptAtts.length;
    byId("excerptsCount").textContent = `Выписок: ${n}`;
    byId<HTMLButtonElement>("excerptsView").disabled = n === 0;
  }

  const closeExcerpts = (): void => { excerptsOverlay.hidden = true; };

  async function openExcerpts(): Promise<void> {
    const body = byId("excerptsBody");
    body.replaceChildren();
    for (const ev of excerptComments(openCardEvents || [])) {
      let text = "";
      try {
        const dk = deps.getDK();
        if (dk) text = await xyCrypto.decField(dk, ev.payload_enc || "");
      } catch (_) {}
      body.append(el("div", { class: "excerpt" },
        el("div", { class: "excerpt-meta", text: `${author(ev)} · ${new Date(ev.created_at).toLocaleString("ru-RU")}` }),
        el("div", { class: "excerpt-text", text })));
    }
    for (const att of openCardExcerptAtts) {
      const name = att.name || "файл";
      const box = el("div", { class: "excerpt" }, el("div", { class: "excerpt-meta", text: `📎 ${name}` }));
      if ((att.mime || "").startsWith("image/")) {
        // .pv-img wires it into the shared lightbox (zoom/pan) on click.
        const img = el("img", { class: "excerpt-img pv-img", alt: name }) as HTMLImageElement;
        deps.attachments.url(att).then((u) => { img.src = u; }).catch(() => {});
        box.append(img);
      } else {
        box.append(el("button", { class: "attach-name", type: "button", text: "Скачать", onclick: () => { void deps.attachments.download(att, name); } }));
      }
      body.append(box);
    }
    excerptsOverlay.hidden = false;
  }

  byId("excerptsView").addEventListener("click", () => { void openExcerpts(); });
  byId("excerptsClose").addEventListener("click", closeExcerpts);
  excerptsOverlay.addEventListener("pointerdown", (e) => { if (e.target === excerptsOverlay) closeExcerpts(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !excerptsOverlay.hidden) closeExcerpts(); });

  return {
    load,
    events: () => openCardEvents,
    setAttachments(atts: AttachmentLike[]): void {
      openCardExcerptAtts = atts.filter((a) => !!a.is_excerpt);
      renderExcerptCount();
    },
  };
}
