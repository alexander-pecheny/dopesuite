// timeline.ts — the card timeline, lifted out of board.js into a typed
// create(deps) factory: event rendering (comments, desc_edit diffs, label +
// attachment events), the brief/full diff preference, the expanded
// full-screen timeline, one-level reply threads, comment edit/delete/excerpt
// and the excerpts overlay. The board injects what it owns (live state, DK, the
// outbox `post` verb, popupMenu, plural, attachment access); the card-detail
// module is reached through the `card` seam (open-card id + comment-link copy),
// which the orchestrator wires back to the carddetail factory's API.
import { modal } from "./modal.js";
import { anchorPopup } from "./popup.js";
import { decodeCommentPayload, encodeCommentPayload } from "./commentpayload.js";
export { decodeCommentPayload, encodeCommentPayload } from "./commentpayload.js";
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { xyDiff } from "./diff.js";
import { xyVersions } from "./versions.js";
import type { AuthMe } from "./app.js";
import type { DataKey } from "./crypto.js";
import type { DiffOp } from "./diff.js";
import type { OpBody, TimelineEvent } from "./store.js";
import { icon, iconed } from "./icons_gen.js";
import S from "./i18nstrings_ru_gen.js";

const { fetchJSON, jpost, jpatch, jdelete, el, onCmdEnter, deriveTitle } = xyApp;

// requestSubmit, not submit(): the form's own submit listener is what posts the
// comment, and a raw .submit() bypasses every listener and navigates the page.
function submitOnCmdEnter(input: HTMLElement, form: HTMLFormElement): void {
  onCmdEnter(input, () => form.requestSubmit());
}

// A timeline event as this module reads it: the synced/pending DTO plus the
// comment-specific flags the server adds and the client-recomputed reply_count.
export interface CardEvent extends TimelineEvent {
  deleted?: boolean;
  edited_at?: string | null;
  is_excerpt?: boolean;
  reply_count?: number;
  // The test this comment came out of, set from its ⋯ menu after the fact.
  session_id?: number | null;
}

// The slice of an attachment the excerpts overlay needs (board's attachment
// DTOs carry more; structural, so they pass through unchanged).
export interface AttachmentLike {
  id: number;
  mime?: string | null;
  name?: string | null;
  is_excerpt?: boolean;
  [key: string]: unknown;
}

// One popupMenu item (board.js's popupMenu contract): `checked` makes it a
// checkbox row, and `radio` makes that checkbox one choice among several.
export interface MenuItem {
  label: string;
  onClick: () => void;
  checked?: boolean;
  radio?: boolean;
  icon?: Node;
  // Starts a new cluster: a rule is drawn above this row (never leading the
  // menu, never doubled), so a conditional item can mark itself without
  // knowing which of its neighbours the scope left in.
  divider?: boolean;
}

// The slice of the board's live state the timeline reads: cards (for the
// card-created line) and the members roster (author names).
export interface TimelineState {
  cards: Array<{ id: number; createdAt: string | null }>;
  me?: AuthMe | null;
  memberNames?: Record<number, string>;
  // The roster (boardmembers.js writes it; mirrored for offline) — what @ can
  // name and what resolveMentions resolves against.
  members?: MentionMember[];
  // The reader's own timeline default (users.feed_default, edited on /profile),
  // delivered in the board snapshot so an offline card open obeys it too.
  feedDefault?: string;
}

// The nodes the timeline works on, resolved once by the page (board.ts).
export interface TimelineUI {
  timeline: HTMLElement;
  cardMessage: HTMLElement;
  commentForm: HTMLFormElement;
  commentInput: HTMLTextAreaElement;
  threadForm: HTMLFormElement;
  threadInput: HTMLTextAreaElement;
  threadBody: HTMLElement;
  threadMessage: HTMLElement;
  feedExpand: HTMLElement;
  feedGrid: HTMLElement;
  feedOrder: HTMLSelectElement;
  feedFilter: HTMLSelectElement;
  feedFilterFull: HTMLSelectElement;
  feedDiffViewRow: HTMLElement;
  feedDiffViewFullRow: HTMLElement;
  feedDiffView: HTMLSelectElement;
  feedDiffViewFull: HTMLSelectElement;
  excerptsView: HTMLButtonElement;
  excerptsCount: HTMLElement;
  excerptsBody: HTMLElement;
}

export interface TimelineDeps {
  ui: TimelineUI;
  getState(): TimelineState;
  getDK(): DataKey | null;
  // The board's outbox `post` verb (see board.js's mutate wrappers) — comments
  // are offline-capable, unlike the edit/delete/excerpt mutations below.
  post(kind: string, path: string, body: OpBody): Promise<unknown>;
  popupMenu(anchor: HTMLElement, items: MenuItem[]): void;
  plural(n: number, one: string, few: string, many: string): string;
  card: {
    openCardId(): number | null;
    copyCommentLink(eventId: number): void;
  };
  // A label's CURRENT name, or "" when it has since been deleted. The payload
  // freezes the name at the time, which was harmless while labels were
  // immutable; now a rename — or a retimed session, whose labels derive their
  // names from it — would leave the whole history reading the old one.
  labelName(labelId: number): string;
  // The test sessions a card is tagged with, as {id, label} — the choices a
  // comment's ⋯ menu offers. Empty offers none.
  cardSessions(cardId: number): Array<{ id: number; label: string }>;
  // One session's CURRENT name, for the badge on a tagged comment: the card it
  // hangs off may since have lost that playing.
  sessionName(sessionId: number): string;
  // Test mode (ADR-0012): the session a comment on this card should be born
  // tagged with, or null. Null when no test is active on this device, and also
  // when the card was hand-unmarked during the mode — an untaggable comment is
  // better than a tag its card's picker cannot reproduce.
  testSession?(cardId: number): number | null;
  // ...and the board's chance to mark the card itself with that session, so
  // the ⋯ menu invariant (a tag is one of the CARD's tests) holds.
  onTestComment?(cardId: number, sessionId: number): void;
  attachments: {
    url(att: AttachmentLike): Promise<string>;
    download(att: AttachmentLike, name: string): Promise<void>;
  };
}

export interface Timeline {
  load(cardId: number): Promise<void>;
  events(): CardEvent[];
  // The board's loadAttachments hands over the fresh attachment list; the
  // timeline keeps the excerpt-flagged ones and refreshes the counter.
  setAttachments(atts: AttachmentLike[]): void;
  // Drops a card's narrowing of the timeline: opening a card starts from the
  // reader's own default, never from what the previous card was left on.
  resetFilter(): void;
  // Which unread watermarks the timeline as currently filtered may clear.
  readBuckets(): { content: boolean; comments: boolean };
  ensureVisible(type: string): Promise<void>;
  // The composer's pending comment ("" when empty) and the shared write path —
  // what the card's Save-on-leave prompt saves.
  commentDraft(): string;
  postComment(): Promise<boolean>;
  // A pasted image, already uploaded as a card attachment, joins the draft.
  addDraftImage(attId: number): void;
  // Discarding from the leave prompt really discards — text and images both.
  clearCommentDraft(): void;
}

// ---- pure decision helpers (exported for tests) ----

// eventVerb words an event kind as a neutral noun phrase — gender-agnostic,
// since an author's grammatical gender is unknown. The timeline and the 🔔 share it.
const EVENT_VERBS: Record<string, string> = {
  comment: S.timeline.event.comment(), desc_edit: S.timeline.event.descEdit(),
  label_add: S.timeline.event.labelAdd(), label_remove: S.timeline.event.labelRemove(),
  attach_add: S.timeline.event.attachAdd(), attach_remove: S.timeline.event.attachRemove(),
  attach_replace: S.timeline.event.attachReplace(), reaction: S.timeline.event.reaction(),
};
export function eventVerb(type: string): string { return EVENT_VERBS[type] || type; }

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
// in the outbox — so the reply-count button would omit one composed offline. The client
// holds the card's WHOLE timeline (deleted replies already filtered out
// server-side), so recounting over the merged list is equivalent online and
// correct offline.
// A reaction anchors to its comment through the same reply_to_id — it is a
// chip, not a reply, so both helpers here count comments only.
export function replyCountsOf(events: ReadonlyArray<{ id: number; type?: string; reply_to_id?: number | null }>): Map<number, number> {
  const replies = new Map<number, number>();
  for (const e of events) {
    if (e.type === "reaction") continue;
    if (e.reply_to_id != null) replies.set(e.reply_to_id, (replies.get(e.reply_to_id) || 0) + 1);
  }
  return replies;
}

// orderThreadReplies picks a root's replies, oldest first. Synced replies order
// by id; un-synced ones are the newest of all but carry NEGATIVE temp ids, so a
// plain id sort would float them to the top. They go last, in the order they
// were queued (-1 queued before -2).
export function orderThreadReplies<T extends { id: number; type?: string; reply_to_id?: number | null }>(
  events: readonly T[],
  rootId: number,
): T[] {
  const all = events.filter((e) => e.reply_to_id === rootId && e.type !== "reaction");
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

// A timeline holds three kinds of entry, and a reader may narrow it to one of them:
// comments (the discussion), edits (desc_edit — what the question used to say)
// and meta (labels and attachments). "all" is every kind.
export type FeedFilter = "all" | "comments" | "edits" | "meta";
const FEED_FILTERS: readonly string[] = ["all", "comments", "edits", "meta"];

export function feedFilterOf(raw: string | null | undefined): FeedFilter {
  return FEED_FILTERS.includes(raw || "") ? (raw as FeedFilter) : "all";
}

export function feedFilterKeeps(type: string, filter: FeedFilter): boolean {
  if (filter === "all") return true;
  if (filter === "comments") return type === "comment";
  if (filter === "edits") return type === "desc_edit";
  return type !== "comment" && type !== "desc_edit";
}

// readBucketsOf: which unread watermarks a timeline read under this filter may
// advance. A reader who narrowed the timeline to comments never saw the edits, so
// their dot must survive. The content bucket is one watermark over both edits
// and meta (migrateV7), so those two cannot be told apart here.
export function readBucketsOf(filter: FeedFilter): { content: boolean; comments: boolean } {
  return { content: filter !== "comments", comments: filter === "all" || filter === "comments" };
}

// linkSegments splits a comment's text into plain runs and URLs so the timeline can
// make links clickable without ever treating user text as markup. Trailing
// sentence punctuation stays outside the link; a ")" is cut only when the URL
// itself opened no "(" (wikipedia-style paths keep theirs).
export function linkSegments(text: string): { text: string; href?: string }[] {
  const out: { text: string; href?: string }[] = [];
  let last = 0;
  for (const m of text.matchAll(/https?:\/\/[^\s<>«»]+/g)) {
    let url = m[0];
    for (;;) {
      const c = url.charAt(url.length - 1);
      if (".,;:!?…'\"".includes(c)) { url = url.slice(0, -1); continue; }
      if (c === ")" && !url.includes("(")) { url = url.slice(0, -1); continue; }
      break;
    }
    if (url.endsWith("//")) continue; // a bare "https://" is not a link
    const start = m.index ?? 0;
    if (start > last) out.push({ text: text.slice(last, start) });
    out.push({ text: url, href: url });
    last = start + url.length;
  }
  if (last < text.length) out.push({ text: text.slice(last) });
  return out;
}

// ---- mentions ----
// A Mention is resolved from the TEXT at submit time (the picker is only typing
// help): every @username that names a board member, checked at both ends so
// «ann@mail.ru» and a prefix of a longer name mention nobody. The resolved ids
// are what the server routes red dots and nudges by (ADR-0009).

const MENTION_CHARS = /[\p{L}\p{N}_.\-]/u;

function mentionSpans(text: string, names: string[]): { start: number; name: string }[] {
  const out: { start: number; name: string }[] = [];
  // Longest first, so @anna never resolves as @ann + "a".
  const sorted = [...names].sort((x, y) => y.length - x.length);
  const taken: boolean[] = [];
  for (const name of sorted) {
    for (let from = 0; (from = text.indexOf("@" + name, from)) !== -1; from += 1) {
      const prev = from === 0 ? "" : text[from - 1];
      const next = text[from + name.length + 1] || "";
      if ((prev && MENTION_CHARS.test(prev)) || (next && MENTION_CHARS.test(next))) continue;
      if (taken.slice(from, from + name.length + 1).some(Boolean)) continue;
      for (let i = from; i < from + name.length + 1; i++) taken[i] = true;
      out.push({ start: from, name });
    }
  }
  return out.sort((x, y) => x.start - y.start);
}

export interface MentionMember { user_id: number; username?: string | null }

export function resolveMentions(text: string, members: readonly MentionMember[]): number[] {
  const byName = new Map<string, number>();
  for (const m of members) if (m.username) byName.set(m.username, m.user_id);
  const out: number[] = [];
  for (const s of mentionSpans(text, [...byName.keys()])) {
    const id = byName.get(s.name)!;
    if (!out.includes(id)) out.push(id);
  }
  return out;
}

// ---- reactions ----
// Reactions aggregate into chips per target (0 = the card itself), never into
// timeline rows. mineId is the caller's own reaction event, the handle for the
// toggle-off DELETE.

export interface ReactionInput { id: number; emoji: string; author: number | null; target: number | null }
export interface ReactionChip { emoji: string; count: number; mineId: number | null; authors: number[] }

export function aggregateReactions(rs: readonly ReactionInput[], meId: number | null): Map<number, ReactionChip[]> {
  const out = new Map<number, ReactionChip[]>();
  for (const r of rs) {
    const key = r.target ?? 0;
    let chips = out.get(key);
    if (!chips) out.set(key, chips = []);
    let chip = chips.find((c) => c.emoji === r.emoji);
    if (!chip) chips.push(chip = { emoji: r.emoji, count: 0, mineId: null, authors: [] });
    chip.count++;
    if (r.author != null && !chip.authors.includes(r.author)) chip.authors.push(r.author);
    if (meId != null && r.author === meId) chip.mineId = r.id;
  }
  return out;
}

// commentBody renders a comment's text: URLs become links, and @names of board
// members become highlighted mentions (never inside a URL — a link stays whole).
export function commentBody(text: string, mentionNames: readonly string[] = [], meName?: string | null): HTMLElement {
  const spans = mentionSpans(text, [...mentionNames]);
  const out: Node[] = [];
  let offset = 0;
  for (const seg of linkSegments(text)) {
    const end = offset + seg.text.length;
    if (seg.href) {
      out.push(el("a", { href: seg.href, target: "_blank", rel: "noopener noreferrer", text: seg.text }));
    } else {
      let cur = offset;
      for (const sp of spans) {
        if (sp.start < cur || sp.start + sp.name.length + 1 > end) continue;
        if (sp.start > cur) out.push(document.createTextNode(text.slice(cur, sp.start)));
        const cls = meName && sp.name === meName ? "tl-mention tl-mention-me" : "tl-mention";
        out.push(el("span", { class: cls, text: "@" + sp.name }));
        cur = sp.start + sp.name.length + 1;
      }
      if (cur < end) out.push(document.createTextNode(text.slice(cur, end)));
    }
    offset = end;
  }
  return el("div", { class: "tl-comment" }, out);
}

// orderFeedEvents: events are oldest→newest (by id), so "newest first" is the
// reverse.
export function orderFeedEvents<T>(events: readonly T[], order: "old" | "new"): T[] {
  return order === "old" ? [...events] : [...events].reverse();
}

// excerptComments picks the comments flagged as excerpts.
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
  const ui = deps.ui;

  const state = deps.getState;

  // openCardEvents mirrors the open card's timeline (set by load) so the
  // expanded timeline, threads, excerpts and the card module's markCardRead can
  // reuse it without a re-fetch.
  let openCardEvents: CardEvent[] = [];
  let openCardAtts: AttachmentLike[] = [];
  let openCardExcerptAtts: AttachmentLike[] = [];
  let threadRootId: number | null = null;
  // Which card the composer's draft (text + pending images) belongs to; a
  // different card starting to load clears it, so a draft never crosses cards.
  let composerCard: number | null = null;

  // The timeline's current narrowing. It starts from the reader's saved default and
  // is dropped when the card closes (resetFilter), so a card always opens the way
  // /profile says — the selects are a look at this card, not a stored preference.
  let filter: FeedFilter = "all";
  // Reactions never make rows — they render as chips on their target.
  const shown = (events: readonly CardEvent[]): CardEvent[] =>
    events.filter((e) => e.type !== "reaction" && feedFilterKeeps(e.type, filter));

  // The open card's decrypted payloads (id → text) and aggregated reaction
  // chips, rebuilt by load(); renderEvent reads both (quotes, chips).
  let payloadOf = new Map<number, string>();
  let openChips = new Map<number, ReactionChip[]>();

  const rosterNames = (): string[] =>
    (state().members || []).map((m) => m.username || "").filter(Boolean);
  const myName = (): string | null => state().me?.username || null;
  const mentionBody = (text: string): HTMLElement => commentBody(text, rosterNames(), myName());

  const feedModal = modal("feed");
  const threadModal = modal("thread");
  const excerptsModal = modal("excerpts");

  const author = (ev: { author_user_id?: number | null }): string => {
    const st = state();
    return eventAuthor(ev, st.me, st.memberNames);
  };

  // ---- timeline ----
  // load renders into a detached fragment and swaps it in once. Emptying
  // the timeline first and appending as the decrypts resolved collapsed the card
  // overlay's scroll height mid-render, so the browser clamped the scroll position
  // and the view jumped up — every flagged excerpt threw the reader back to
  // the excerpts counter. The container must never be shorter than its content.
  // painted names the card the timeline currently shows, and loadSeq the newest
  // load in flight. Between them they keep one card's comments off another
  // card: the skeleton covers the wait, the sequence drops a slow load whose
  // card is no longer open (open A, open B, A's fetch lands last).
  let painted: number | null = null;
  let loadSeq = 0;

  // skeleton is what the timeline shows while the next card's comments are being
  // fetched and decrypted — a second or two on a busy card. Leaving the previous
  // card's timeline up reads as the wrong comments rather than as loading, and
  // simply emptying it is what the note above forbids: three grey rows keep the
  // container tall while saying "not yet".
  function skeleton(): DocumentFragment {
    const frag = document.createDocumentFragment();
    for (let i = 0; i < 3; i++) {
      frag.append(el("div", { class: "tl-event tl-skeleton" },
        el("div", { class: "tl-skeleton-bar tl-skeleton-meta" }),
        el("div", { class: "tl-skeleton-bar" }),
        el("div", { class: "tl-skeleton-bar tl-skeleton-short" })));
    }
    return frag;
  }

  async function load(cardId: number): Promise<void> {
    const tl = ui.timeline;
    const seq = ++loadSeq;
    // Only when the card changes: a reload of the card already on screen (a
    // posted comment, a new attachment) must not blink, and the timeline may not
    // get shorter under a reader who has scrolled it.
    if (painted !== cardId) { painted = cardId; tl.replaceChildren(skeleton()); }
    if (composerCard !== cardId) { composerCard = cardId; clearCommentDraft(); }
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
    // Every payload is decrypted up front: a reply quotes its parent and a
    // reaction chip needs its emoji, whichever of the two the filter shows.
    const payloads = new Map<number, string>();
    for (const ev of events) {
      try {
        const dk = deps.getDK();
        if (dk) payloads.set(ev.id, await xyCrypto.decField(dk, ev.payload_enc || ""));
      } catch (_) {}
    }
    if (cardId === deps.card.openCardId()) {
      openCardEvents = events;
      payloadOf = payloads;
      openChips = aggregateReactions(
        events.filter((e) => e.type === "reaction" && !e.deleted).map((e) => ({
          id: e.id, emoji: payloads.get(e.id) || "", author: e.author_user_id ?? null, target: e.reply_to_id ?? null,
        })).filter((r) => r.emoji),
        state().me?.user_id ?? null);
      renderExcerptCount();
    }
    // Newest first: events are oldest→newest (by id); show them reversed.
    const frag = document.createDocumentFragment();
    frag.append(cardReactionsRow());
    for (const ev of shown(events).reverse()) {
      frag.append(renderEvent(ev, payloads.get(ev.id) || ""));
    }
    // Oldest goes last in the newest-first timeline.
    const born = cardCreatedNode(cardId);
    if (born) frag.append(born);
    if (seq !== loadSeq) return; // a newer load owns the timeline now
    tl.replaceChildren(frag);
  }

  // ---- reactions (chips + picker) ----
  const QUICK_EMOJI = ["👍", "❤️", "🔥", "😂", "🤔", "👏"];

  // toggleReaction: my chip off, anything else on. Online-only, like every
  // comment mutation but the comment itself.
  async function toggleReaction(targetId: number | null, emoji: string): Promise<void> {
    const oc = deps.card.openCardId();
    if (!oc) return;
    const msg = ui.cardMessage;
    if (!xySync.requireOnline(S.timeline.reaction.offline(), msg)) return;
    const mine = (openChips.get(targetId ?? 0) || []).find((c) => c.emoji === emoji)?.mineId;
    try {
      if (mine) await jdelete(`/api/reactions/${mine}`);
      else {
        await jpost(`/api/cards/${oc}/reactions`, {
          payload_enc: await xyCrypto.encField(mustDK(), emoji), target_id: targetId,
        });
      }
      msg.textContent = "";
      await refreshFeeds();
    } catch (err) { msg.textContent = err instanceof Error ? err.message : String(err); }
  }

  function openReactionPicker(anchor: HTMLElement, targetId: number | null): void {
    const items: MenuItem[] = QUICK_EMOJI.map((emoji) => ({
      label: emoji, onClick: () => { void toggleReaction(targetId, emoji); },
    }));
    items.push({
      label: S.timeline.reaction.other(),
      onClick: () => {
        const raw = (prompt(S.timeline.reaction.emojiPrompt()) || "").trim();
        if (raw) void toggleReaction(targetId, raw);
      },
    });
    deps.popupMenu(anchor, items);
  }

  // chipsRow renders a target's chips (click = toggle) plus the add button.
  // targetId null = the card itself.
  function chipsRow(targetId: number | null, always: boolean): HTMLElement | null {
    const chips = openChips.get(targetId ?? 0) || [];
    if (!chips.length && !always) return null;
    const row = el("div", { class: "tl-chips" });
    const names = state().memberNames || {};
    for (const c of chips) {
      const who = c.authors.map((a) => names[a] || `#${a}`).join(", ");
      row.append(el("button", {
        class: "tl-chip" + (c.mineId ? " tl-chip-mine" : ""), type: "button", title: who,
        text: c.count > 1 ? `${c.emoji} ${c.count}` : c.emoji,
        onclick: () => { void toggleReaction(targetId, c.emoji); },
      }));
    }
    const add = el("button", { class: "tl-chip tl-chip-add", type: "button", title: S.timeline.reaction.addTitle() }, icon("plus"));
    add.addEventListener("click", () => openReactionPicker(add, targetId));
    row.append(add);
    return row;
  }

  function cardReactionsRow(): HTMLElement {
    return el("div", { class: "tl-card-reactions" }, chipsRow(null, true));
  }

  // commentImageNodes renders a comment's referenced attachments inline;
  // .pv-img wires each into the shared lightbox. The real attachment record is
  // looked up when known — the byte cache is keyed by (id, rev), so a bare
  // {id} would pin the first revision forever after a replace.
  function commentImageNodes(ids: readonly number[]): HTMLElement[] {
    return ids.map((id) => {
      const att = openCardAtts.find((a) => a.id === id) || { id };
      const img = el("img", { class: "tl-comment-img pv-img", alt: S.timeline.comment.imageAlt() }) as HTMLImageElement;
      deps.attachments.url(att).then((u) => { img.src = u; }).catch(() => { img.remove(); });
      return img;
    });
  }

  // cardCreatedNode is the card-created line closing the timeline — the anchor
  // every later timestamp is read against. It is derived from cards.created_at
  // rather than from a timeline event, so it is there for every card ever made,
  // not just ones created after this shipped.
  function cardCreatedNode(cardId: number | null): HTMLElement | null {
    const card = state().cards.find((c) => c.id === cardId);
    if (!card || !card.createdAt) return null;
    return el("div", { class: "tl-event tl-born" },
      el("div", { class: "tl-meta", text: S.timeline.feed.cardCreated(new Date(card.createdAt).toLocaleString("ru-RU")) }));
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
        const row = el("div", { class: "tl-meta" }, S.timeline.comment.deleted());
        if ((ev.reply_count || 0) > 0) row.append(threadButton(ev));
        wrap.append(row);
        return wrap;
      }
      let quoteNode: HTMLElement | null = null;
      const metaRow = el("div", { class: "tl-meta" }, meta(when + (ev.edited_at ? S.timeline.comment.editedSuffix() : "")));
      if (ev.is_excerpt) {
        wrap.classList.add("tl-excerpt");
        metaRow.append(el("span", { class: "tl-badge", text: S.timeline.excerpt.badge() }));
      }
      // Which test it came out of, since that is now set from the ⋯ menu and
      // would otherwise be visible only from inside that menu.
      if (ev.session_id != null) {
        metaRow.append(el("span", { class: "tl-badge tl-badge-session" }, icon("flask-conical"), deps.sessionName(ev.session_id)));
      }
      // A reply keeps its place in the flat timeline (it is part of the card's
      // history) but says what it answers, and links up to it. Added BEFORE
      // .tl-actions, which is margin-left:auto and would otherwise push this to
      // the far right of the row.
      if (ev.reply_to_id) {
        const rootId = ev.reply_to_id;
        const parent = (openCardEvents || []).find((e) => e.id === rootId);
        const parentWho = parent ? (parent.deleted ? S.timeline.reply.deletedParent() : author(parent)) : S.timeline.reply.unknownParent();
        metaRow.append(el("span", { class: "tl-sep", text: "·" }), el("button", {
          class: "tl-replyto", type: "button", title: S.timeline.thread.openTitle(),
          text: S.timeline.reply.inReplyTo(parentWho), onclick: () => { void openThread(rootId); },
        }));
        // The parent's first line and a half, so the answer carries its
        // question — old replies included, since this is derived at render.
        const parentText = decodeCommentPayload(payloadOf.get(rootId) || "").text;
        if (parent && !parent.deleted && parentText) {
          quoteNode = el("button", {
            class: "tl-quote", type: "button", title: S.timeline.thread.openTitle(),
            text: deriveTitle(parentText, 110), onclick: () => { void openThread(rootId); },
          });
        }
      }
      // Synced comments have a stable event id → offer a copyable direct link, the
      // edit/delete/excerpt menu, and an anchor target. Pending (offline) comments
      // have no id yet, so none of that can address them.
      if (ev.id) {
        wrap.id = "tlev-" + ev.id;
        // Right-anchored so both controls sit in the comment's top-right corner
        // rather than trailing a timestamp of unpredictable width.
        metaRow.append(el("div", { class: "tl-actions" },
          el("button", {
            class: "tl-link", type: "button", title: S.timeline.comment.copyLinkTitle(),
            onclick: () => deps.card.copyCommentLink(ev.id),
          }, icon("link")),
          el("button", {
            class: "tl-menu", type: "button", title: S.timeline.comment.menuTitle(), "aria-haspopup": "true",
            onclick: (e: Event) => commentMenu(e.currentTarget as HTMLElement, ev, payload),
          }, icon("ellipsis"))));
      }
      const decoded = decodeCommentPayload(payload);
      wrap.append(metaRow);
      if (quoteNode) wrap.append(quoteNode);
      wrap.append(mentionBody(decoded.text));
      for (const img of commentImageNodes(decoded.images)) wrap.append(img);
      const chips = ev.id > 0 ? chipsRow(ev.id, false) : null;
      if (chips) wrap.append(chips);
      if ((ev.reply_count || 0) > 0) wrap.append(threadButton(ev));
    } else if (ev.type === "desc_edit") {
      let diff: { before?: string; after?: string; author?: string } = {};
      try { diff = JSON.parse(payload) as { before?: string; after?: string; author?: string }; } catch (_) {}
      // An imported edit (Trello history) names its author inside the payload —
      // they are not an xy user, so author_user_id has nobody to point at.
      const editor = diff.author ? `${diff.author} · ` : meta("");
      wrap.append(el("div", { class: "tl-meta", text: editor + S.timeline.feed.descEditMeta(when) }),
        renderDescDiff(diff.before || "", diff.after || ""));
    } else {
      let info: { label?: string; file?: string; label_id?: number } = {};
      try { info = JSON.parse(payload) as { label?: string; file?: string; label_id?: number }; } catch (_) {}
      const verb = eventVerb(ev.type);
      // Live name when the label still exists, the frozen one when it doesn't —
      // which keeps a deleted label's history readable, the property freezing the
      // name was there to protect.
      const live = info.label_id != null ? deps.labelName(info.label_id) : "";
      const detail = live || info.label || info.file || "";
      wrap.append(el("div", { class: "tl-meta", text: meta(`${verb}${detail ? ": " + detail : ""} · ${when}`) }));
    }
    return wrap;
  }

  // ---- desc_edit rendering: brief / full ----
  // A card's description is long and an edit usually touches a few words, so the
  // default (brief) shows just those with a little context. full is the
  // original two-pane before/after, kept for when the whole text matters. The
  // choice is a per-reader display preference, so it lives in localStorage beside
  // the other display prefs rather than on the server.
  const DIFF_VIEW_KEY = "xy.diffView";
  function diffView(): "full" | "brief" {
    return diffViewOf(localStorage.getItem(DIFF_VIEW_KEY));
  }

  // renderDescDiff diffs each version against its own counterpart rather than the
  // card against the card. The versions of one card are near-duplicates, and a
  // token diff let loose across all of them latches version 2's words onto
  // version 1's and reports a change nobody made. Versions are paired by
  // position, so an added version reads as one addition.
  function renderDescDiff(before: string, after: string): HTMLElement {
    const b = xyVersions.splitVersions(before), a = xyVersions.splitVersions(after);
    const one = (x: string, y: string): HTMLElement => {
      const ops = xyDiff.diffTokens(x, y);
      return diffView() === "brief" ? renderBriefDiff(ops) : renderFullDiff(ops);
    };
    if (b.length <= 1 && a.length <= 1) return one(before, after);
    const box = el("div", { class: "tl-versions" });
    for (let i = 0; i < Math.max(b.length, a.length); i++) {
      const name = xyVersions.versionName(after, i) ?? xyVersions.versionName(before, i);
      box.append(
        el("div", { class: "tl-vname", text: name || S.timeline.diff.versionFallback(String(i + 1)) }),
        one(b[i] ?? "", a[i] ?? ""),
      );
    }
    return box;
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
    if (!(box.textContent || "").trim()) box.append(el("span", { class: "tl-gap", text: S.timeline.diff.noChanges() }));
    return box;
  }

  // setDiffView keeps the two selects (card + expanded timeline) in step and
  // re-renders whichever feeds are on screen.
  async function setDiffView(v: string): Promise<void> {
    localStorage.setItem(DIFF_VIEW_KEY, v === "full" ? "full" : "brief");
    for (const sel of [ui.feedDiffView, ui.feedDiffViewFull]) sel.value = diffView();
    const oc = deps.card.openCardId();
    if (oc) await load(oc);
    if (feedModal.isOpen) await renderFeedGrid();
  }

  for (const sel of [ui.feedDiffView, ui.feedDiffViewFull]) {
    sel.value = diffView();
    sel.addEventListener("change", () => { void setDiffView(sel.value); });
  }

  // ---- show: which kind of entry the timeline shows ----
  // Both selects and the two diff-view rows follow one value: a diff-view
  // control governs nothing when no edits are on screen.
  async function setFilter(v: string, reload: boolean): Promise<void> {
    filter = feedFilterOf(v);
    for (const sel of [ui.feedFilter, ui.feedFilterFull]) sel.value = filter;
    const showsEdits = feedFilterKeeps("desc_edit", filter);
    for (const row of [ui.feedDiffViewRow, ui.feedDiffViewFullRow]) row.hidden = !showsEdits;
    if (!reload) return;
    const oc = deps.card.openCardId();
    if (oc) await load(oc);
    if (feedModal.isOpen) await renderFeedGrid();
  }

  for (const sel of [ui.feedFilter, ui.feedFilterFull]) {
    sel.addEventListener("change", () => { void setFilter(sel.value, true); });
  }

  // ---- expanded timeline ----
  // The card panel gives the timeline ~320px of height; on a long discussion that is
  // a keyhole. Expand re-renders the same events full-screen, flowed into
  // columns so as much as possible is readable at once.
  // Reading order in the expanded timeline. The panel's feed is always newest-first
  // (you go there for what just happened); reading a whole discussion end to end
  // is the other job, and that one wants oldest-first.
  const FEED_ORDER_KEY = "xy.feedOrder";
  function feedOrder(): "old" | "new" {
    return feedOrderOf(localStorage.getItem(FEED_ORDER_KEY));
  }

  async function renderFeedGrid(): Promise<void> {
    const grid = ui.feedGrid;
    const frag = document.createDocumentFragment();
    // openCardEvents is oldest→newest (by id), so "newest first" is the reverse.
    const ordered = orderFeedEvents(shown(openCardEvents || []), feedOrder());
    for (const ev of ordered) {
      let payload = "";
      if (!ev.deleted) {
        try {
          const dk = deps.getDK();
          if (dk) payload = await xyCrypto.decField(dk, ev.payload_enc || "");
        } catch (_) {}
      }
      const node = renderEvent(ev, payload);
      // The panel's timeline already owns tlev-{id}; these are a SECOND rendering of
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

  const feedOrderSel = ui.feedOrder;
  feedOrderSel.value = feedOrder();
  feedOrderSel.addEventListener("change", async () => {
    localStorage.setItem(FEED_ORDER_KEY, feedOrderSel.value === "old" ? "old" : "new");
    if (feedModal.isOpen) await renderFeedGrid();
  });

  ui.feedExpand.addEventListener("click", async () => {
    feedModal.open();
    await renderFeedGrid();
  });

  // ---- reply threads ----
  // Threads are one level deep and live in a modal: the timeline stays flat and
  // newest-first (it is a history), while a thread reads oldest-first (it is a
  // conversation). Replies appear in BOTH — the timeline never hides a comment.
  function threadButton(ev: CardEvent): HTMLElement {
    const n = ev.reply_count || 0;
    return el("button", {
      class: "tl-thread", type: "button",
      onclick: () => { void openThread(ev.id); },
    }, ...iconed("message-circle", S.timeline.thread.replies(n)));
  }

  // openThread renders the root comment and its replies, oldest first, from the
  // events already loaded for the open card — no extra round trip.
  async function openThread(rootId: number): Promise<void> {
    threadRootId = rootId;
    const events = openCardEvents || [];
    const root = events.find((e) => e.id === rootId);
    const replies = orderThreadReplies(events, rootId);
    const body = ui.threadBody;
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
      const decoded = decodeCommentPayload(text);
      const node = el("div", { class: "thread-item" + (ev.id === rootId ? " thread-root" : "") },
        el("div", { class: "tl-meta" },
          ev.deleted ? S.timeline.comment.deleted()
            : `${author(ev)} · ${new Date(ev.created_at).toLocaleString("ru-RU")}${ev.edited_at ? S.timeline.comment.editedSuffix() : ""}`,
          ev.is_excerpt ? el("span", { class: "tl-badge", text: S.timeline.excerpt.badge() }) : null),
        ev.deleted ? null : mentionBody(decoded.text),
        ...(ev.deleted ? [] : commentImageNodes(decoded.images)));
      frag.append(node);
    }
    body.replaceChildren(frag);
    threadModal.open({ onClose: () => { threadRootId = null; } });
  }

  submitOnCmdEnter(ui.threadInput, ui.threadForm);
  ui.threadForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    const input = ui.threadInput;
    const text = input.value.trim();
    const oc = deps.card.openCardId();
    if (!text || !oc || !threadRootId) return;
    const msg = ui.threadMessage;
    try {
      const dk = mustDK();
      const sid = deps.testSession?.(oc) ?? null;
      await deps.post("comment", `/api/cards/${oc}/comments`, {
        payload_enc: await xyCrypto.encField(dk, text), reply_to_id: threadRootId,
        mentions: resolveMentions(text, state().members || []),
        ...(sid != null ? { session_id: sid } : {}),
      });
      if (sid != null) deps.onTestComment?.(oc, sid);
      input.value = "";
      await load(oc);
      await openThread(threadRootId); // re-render the thread with the new reply
    } catch (err) { msg.textContent = err instanceof Error ? err.message : String(err); }
  });


  function mustDK(): DataKey {
    const dk = deps.getDK();
    if (!dk) throw new Error("нет ключа доски");
    return dk;
  }

  // ---- comment edit / delete / excerpt ----
  // Rewriting or removing a comment is the author's business; flagging one as an
  // excerpt is curation any member may do (the server draws the same line in
  // handlePatchComment). All three are online-only, like attachment mutations: a
  // queued edit of a comment that has not itself synced yet is a temp-id knot the
  // outbox has no reason to learn.
  function commentMenu(anchor: HTMLElement, ev: CardEvent, payload: string): void {
    const st = state();
    const mine = !!(st.me && ev.author_user_id === st.me.user_id);
    // Replying opens the thread (with its composer) — for a comment with no
    // replies yet, that is just the comment plus an empty answer box.
    const items: MenuItem[] = [{ icon: icon("message-circle"), label: S.timeline.comment.menuReply(), onClick: () => { void openThread(ev.reply_to_id || ev.id); } }];
    // A comment's FIRST reaction has no chip to click yet — this is its way in.
    items.push({ icon: icon("plus"), label: S.timeline.comment.menuReact(), onClick: () => openReactionPicker(anchor, ev.id) });
    if (mine) {
      // The node is taken from the anchor, not looked up by id: the same comment
      // may also be rendered in the expanded timeline, and the edit must open on the
      // copy whose ⋯ was actually clicked.
      items.push({ icon: icon("pencil"), label: S.timeline.comment.menuEdit(), onClick: () => startCommentEdit(ev, payload, anchor.closest<HTMLElement>(".tl-event")) });
      items.push({ icon: icon("trash-2"), label: S.timeline.comment.menuDelete(), onClick: () => deleteComment(ev) });
    }
    items.push({
      label: S.timeline.comment.menuExcerpt(), checked: !!ev.is_excerpt,
      onClick: () => { void commentAction(() => jpatch(`/api/comments/${ev.id}`, { is_excerpt: !ev.is_excerpt })); },
    });
    // The test this came out of, named after the fact — the sitting where the
    // team stumbled over the wording. Radio, not checkboxes: a comment came out of
    // one sitting, unlike the card, which is played at several. 0 clears it.
    // A comment queued offline has no card_id of its own yet.
    const cardId = typeof ev.card_id === "number" ? ev.card_id : deps.card.openCardId();
    const sessions = cardId != null ? deps.cardSessions(cardId) : [];
    if (sessions.length) {
      const tag = (id: number): void => {
        void commentAction(() => jpatch(`/api/comments/${ev.id}`, { session_id: id }));
      };
      items.push({ label: S.timeline.comment.menuNoTest(), checked: ev.session_id == null, radio: true, onClick: () => tag(0) });
      for (const s of sessions) {
        items.push({ label: s.label, checked: ev.session_id === s.id, radio: true, onClick: () => tag(s.id) });
      }
    }
    deps.popupMenu(anchor, items);
  }

  // commentAction runs one comment mutation and re-renders the timeline (which also
  // refreshes the excerpts counter), reporting failures in the card's message line.
  async function commentAction(fn: () => Promise<unknown>): Promise<void> {
    const msg = ui.cardMessage;
    if (!xySync.requireOnline(S.timeline.comment.offline(), msg)) return;
    try {
      await fn();
      msg.textContent = "";
      await refreshFeeds();
    } catch (err) { msg.textContent = err instanceof Error ? err.message : String(err); }
  }

  // refreshFeeds re-renders both places a comment can appear: the card panel's
  // timeline and, when open, the expanded one.
  async function refreshFeeds(): Promise<void> {
    const oc = deps.card.openCardId();
    if (oc != null) await load(oc);
    if (feedModal.isOpen) await renderFeedGrid();
  }

  function deleteComment(ev: CardEvent): void {
    if (!confirm(S.timeline.comment.deleteConfirm())) return;
    void commentAction(() => jdelete(`/api/comments/${ev.id}`));
  }

  // startCommentEdit swaps the comment's body for a textarea in place, so the
  // surrounding timeline stays put while it is edited.
  function startCommentEdit(ev: CardEvent, payload: string, wrap: HTMLElement | null): void {
    if (!wrap || wrap.querySelector(".tl-edit")) return;
    const body = wrap.querySelector(".tl-comment");
    if (!body) return;
    const ta = el("textarea", { class: "card-desc comment-input tl-edit", spellcheck: "false" }) as HTMLTextAreaElement;
    // The textarea edits the TEXT; images the comment carries ride along
    // untouched, and mentions are re-resolved from what the text now says.
    const decoded = decodeCommentPayload(payload);
    ta.value = decoded.text;
    const save = el("button", {
      class: "btn btn-small", type: "button", text: S.timeline.comment.save(),
      onclick: async () => {
        const text = ta.value.trim();
        if (!text) return;
        await commentAction(async () => jpatch(`/api/comments/${ev.id}`, {
          payload_enc: await xyCrypto.encField(mustDK(), encodeCommentPayload(text, decoded.images)),
          mentions: resolveMentions(text, state().members || []),
        }));
      },
    });
    const cancel = el("button", {
      class: "btn btn-small btn-ghost", type: "button", text: S.timeline.comment.cancel(),
      onclick: () => { void refreshFeeds(); },
    });
    body.replaceWith(el("div", { class: "tl-editbox" }, ta, el("div", { class: "tl-editrow" }, save, cancel)));
    ta.focus();
  }

  // ---- the main composer ----
  // Images pasted into the comment box become card attachments referenced by
  // the payload; the chips row under the box is the pending list.
  let draftImages: number[] = [];
  const draftImagesRow = el("div", { class: "tl-draft-imgs", hidden: true });
  ui.commentInput.insertAdjacentElement("afterend", draftImagesRow);

  function renderDraftImages(): void {
    draftImagesRow.replaceChildren();
    draftImagesRow.hidden = !draftImages.length;
    for (const id of draftImages) {
      const chip = el("span", { class: "tl-draft-img" });
      const img = el("img", { alt: "" }) as HTMLImageElement;
      deps.attachments.url({ id }).then((u) => { img.src = u; }).catch(() => {});
      chip.append(img, el("button", {
        class: "attach-del", type: "button", title: S.timeline.comment.removeImageTitle(), text: "×",
        onclick: () => { draftImages = draftImages.filter((x) => x !== id); renderDraftImages(); },
      }));
      draftImagesRow.append(chip);
    }
  }

  function addDraftImage(attId: number): void {
    if (!draftImages.includes(attId)) draftImages.push(attId);
    renderDraftImages();
  }

  function clearCommentDraft(): void {
    ui.commentInput.value = "";
    draftImages = [];
    renderDraftImages();
  }

  function commentDraft(): string {
    return ui.commentInput.value.trim() || (draftImages.length ? S.timeline.comment.imageDraft() : "");
  }

  // postComment is the one write path: the submit handler and the card's
  // Save-on-leave prompt both land here.
  async function postComment(): Promise<boolean> {
    const input = ui.commentInput;
    const text = input.value.trim();
    const oc = deps.card.openCardId();
    if ((!text && !draftImages.length) || !oc) return false;
    try {
      // In test mode a comment is born tagged (ADR-0012); otherwise it is
      // tagged from its own ⋯ menu once it exists.
      const sid = deps.testSession?.(oc) ?? null;
      await deps.post("comment", `/api/cards/${oc}/comments`, {
        payload_enc: await xyCrypto.encField(mustDK(), encodeCommentPayload(text, draftImages)),
        mentions: resolveMentions(text, state().members || []),
        ...(sid != null ? { session_id: sid } : {}),
      });
      if (sid != null) deps.onTestComment?.(oc, sid);
      input.value = "";
      draftImages = [];
      renderDraftImages();
      await load(oc);
      return true;
    } catch (err) {
      ui.cardMessage.textContent = err instanceof Error ? err.message : String(err);
      return false;
    }
  }

  submitOnCmdEnter(ui.commentInput, ui.commentForm);
  ui.commentForm.addEventListener("submit", (e) => {
    e.preventDefault();
    void postComment();
  });

  // ---- @-mentions typing help ----
  // A dropdown of member names once the caret sits in an @-token. Picking
  // inserts through execCommand so the browser's undo stack survives (the
  // repo-wide rule for programmatic edits).
  function attachMentionPicker(input: HTMLTextAreaElement | HTMLInputElement): void {
    let popup: { close(): void } | null = null;
    const closePopup = (): void => { popup?.close(); popup = null; };
    input.addEventListener("blur", () => setTimeout(closePopup, 150));
    input.addEventListener("keydown", (e: Event) => {
      if ((e as KeyboardEvent).key === "Escape" && popup) { e.stopPropagation(); closePopup(); }
    });
    input.addEventListener("input", () => {
      closePopup();
      const caret = input.selectionStart ?? input.value.length;
      const head = input.value.slice(0, caret);
      const m = head.match(/(^|\s)@([\p{L}\p{N}_.\-]*)$/u);
      if (!m) return;
      const token = m[2];
      const names = rosterNames().filter((n) => n.toLowerCase().startsWith(token.toLowerCase()) && n !== token);
      if (!names.length) return;
      const list = el("div", { class: "menu-dropdown menu-fixed mention-menu" });
      for (const name of names.slice(0, 8)) {
        const btn = el("button", { class: "menu-item", type: "button", text: "@" + name });
        // pointerdown + preventDefault, so the textarea never blurs (the
        // suggestWrap trick) and the insert lands in one undo step.
        btn.addEventListener("pointerdown", (e) => {
          e.preventDefault();
          input.setSelectionRange(caret - token.length - 1, caret);
          input.focus();
          document.execCommand("insertText", false, "@" + name + " ");
          closePopup();
        });
        list.append(btn);
      }
      popup = anchorPopup(list, input, { align: "start", onClose: () => { popup = null; } });
    });
  }
  attachMentionPicker(ui.commentInput);
  attachMentionPicker(ui.threadInput);

  // ---- excerpts ----
  // An excerpt is a passage from a source — a comment or an attachment flagged as
  // such — so the sources behind a question can be re-read mid-edit without
  // scrolling the whole timeline or opening attachments one browser tab at a time.
  // The flag is a plaintext column server-side (migrateV14); the content is not.
  function renderExcerptCount(): void {
    const n = excerptComments(openCardEvents || []).length + openCardExcerptAtts.length;
    ui.excerptsCount.textContent = S.timeline.excerpt.count(String(n));
    ui.excerptsView.disabled = n === 0;
  }

  async function openExcerpts(): Promise<void> {
    const body = ui.excerptsBody;
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
      const name = att.name || S.timeline.excerpt.fileFallback();
      const box = el("div", { class: "excerpt" }, el("div", { class: "excerpt-meta" }, ...iconed("paperclip", name)));
      if ((att.mime || "").startsWith("image/")) {
        // .pv-img wires it into the shared lightbox (zoom/pan) on click.
        const img = el("img", { class: "excerpt-img pv-img", alt: name }) as HTMLImageElement;
        deps.attachments.url(att).then((u) => { img.src = u; }).catch(() => {});
        box.append(img);
      } else {
        box.append(el("button", { class: "attach-name", type: "button", text: S.timeline.excerpt.download(), onclick: () => { void deps.attachments.download(att, name); } }));
      }
      body.append(box);
    }
    excerptsModal.open();
  }

  ui.excerptsView.addEventListener("click", () => { void openExcerpts(); });

  return {
    load,
    events: () => openCardEvents,
    setAttachments(atts: AttachmentLike[]): void {
      openCardAtts = atts;
      openCardExcerptAtts = atts.filter((a) => !!a.is_excerpt);
      renderExcerptCount();
    },
    resetFilter(): void { void setFilter(state().feedDefault || "all", false); },
    readBuckets: () => readBucketsOf(filter),
    // A link INTO the timeline — a 🔔 row, a ?comment= deep link — must land on the
    // entry it names, whatever the reader's default hides.
    async ensureVisible(type: string): Promise<void> {
      if (!feedFilterKeeps(type, filter)) await setFilter("all", true);
    },
    commentDraft,
    postComment,
    addDraftImage,
    clearCommentDraft,
  };
}
