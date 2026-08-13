// searchindex.ts — the Search Index: what this device can FIND, as opposed to
// what it can merely open. One record per board, holding each card's 4s, its
// alias and the comments on it — decrypted (ADR-0008: the raw data key already
// sits in "xy-keys", so plaintext beside it adds no exposure, and the index dies
// with the key).
//
// Written only by code that already holds a data key: the board page as it loads
// and edits (putCards), and прогрев (prewarm), which fills the Mirror for every
// board this device can unlock so search coverage stops depending on which
// boards happened to be opened. The searching itself is pure and lives below.
//
// ES module.

import { xyApp } from "./app.js";
import { xyChgk } from "./chgk.js";
import { xyCrypto } from "./crypto.js";
import type { DataKey } from "./crypto.js";
import { byRank } from "./dragrank.js";
import { decodeCommentPayload } from "./timeline.js";
import { xyFind } from "./find.js";
import type { Haystack, Snippet } from "./find.js";
import { xyStore } from "./store.js";
import type { BoardSnapshot } from "./store.js";
import { xySync } from "./sync.js";

export interface IndexList { id: number; title: string }
export interface IndexCard { id: number; list: number; kind: string; desc: string; alias: string }
export interface IndexComment { card: number; id: number; text: string }

export interface BoardIndex {
  name: string;
  lists: IndexList[];
  cards: IndexCard[];
  comments: IndexComment[];
}

// One result as a tile shows it.
export interface Hit {
  board: number;
  boardName: string;
  list: string;
  card: number;
  title: string;
  snippet: Snippet;
  // Further matches in the same place, whichever field they fall in.
  more: number;
  // Set on a comment hit: the entry the tile deep-links to.
  comment?: number;
}

// A question and the discussion about it are two different answers to «где это
// было?», so they are counted and listed apart. Each half caps independently —
// a needle that names a hundred questions must not bury the one comment.
export interface SearchResult {
  questions: Hit[];
  comments: Hit[];
  questionTotal: number;
  commentTotal: number;
}

const empty = (name = ""): BoardIndex => ({ name, lists: [], cards: [], comments: [] });

// ---- storage ----

// Every write here is a read-modify-write of one record, and two of them run
// concurrently on board open (the render path writes the cards while прогрев or
// refreshComments awaits the network). Serialised per board, the later one reads
// what the earlier one wrote instead of clobbering it.
const writing = new Map<number, Promise<unknown>>();

function serially<T>(boardId: number, fn: () => Promise<T>): Promise<T> {
  const done = (writing.get(boardId) || Promise.resolve()).then(fn, fn);
  writing.set(boardId, done.catch(() => undefined));
  return done;
}

export async function load(boardId: number): Promise<BoardIndex | null> {
  const idx = (await xyStore.getIndex(boardId).catch(() => undefined)) as BoardIndex | undefined;
  return idx || null;
}

export async function all(): Promise<Array<{ board: number; index: BoardIndex }>> {
  const rows = await xyStore.allIndexes().catch(() => []);
  return rows.map((r) => ({ board: r.board, index: r.index as BoardIndex }));
}

// forget drops a board's index. Called wherever its key is dropped — «Забыть
// пароль доски» and board deletion — because an index that outlived the key
// would keep every question readable with none.
export async function forget(boardId: number): Promise<void> {
  await xyStore.deleteIndex(boardId).catch(() => undefined);
}

// putCards writes the half the board page owns: the names, the lists and the
// cards, in board order. The comments are left as they were — the board page
// never holds them all, and прогрев is what fills them.
export async function putCards(
  boardId: number,
  name: string,
  lists: IndexList[],
  cards: IndexCard[],
): Promise<void> {
  await serially(boardId, async () => {
    const prev = (await load(boardId)) || empty();
    const live = new Set(cards.map((c) => c.id));
    await xyStore.putIndex(boardId, {
      name,
      lists,
      cards,
      comments: prev.comments.filter((c) => live.has(c.card)),
    });
  }).catch(() => undefined);
}

// refreshComments pulls every comment on the board in one request and stores the
// plaintext. Online-only and best-effort: the comments are a bonus, the cards
// are indexed either way.
export async function refreshComments(boardId: number, dk: DataKey): Promise<void> {
  if (!xySync.isOnline()) return;
  try {
    const rows = (await xyApp.fetchJSON(`/api/boards/${boardId}/comments`)) as CommentRow[];
    const comments = await decryptComments(dk, rows);
    await serially(boardId, async () => {
      const prev = (await load(boardId)) || empty();
      await xyStore.putIndex(boardId, { ...prev, comments });
    });
  } catch (_) { /* nothing to do: the index keeps the comments it had */ }
}

// ---- building from ciphertext ----

// decrypt turns a board's snapshot into the plaintext halves of an index. Cards
// come out in board order — lists by rank, cards by rank within a list — so the
// result list reads in the order the board does.
export async function decrypt(dk: DataKey, snap: BoardSnapshot): Promise<{ lists: IndexList[]; cards: IndexCard[] }> {
  const dec = async (b64: string | null | undefined): Promise<string> => {
    if (!b64) return "";
    try { return await xyCrypto.decField(dk, b64); } catch (_) { return ""; }
  };
  // byRank, not localeCompare: a rank is a base-62 fractional index and only
  // codepoint order sorts it right («aA» sorts before «aa», which a locale
  // comparison reverses).
  const ranked = (v: { rank?: unknown }): { rank: string } => ({ rank: String(v.rank ?? "") });
  const rawLists = [...(snap.lists || [])].sort((a, b) => byRank(ranked(a), ranked(b)));
  const lists: IndexList[] = [];
  for (const l of rawLists) lists.push({ id: l.id, title: await dec(l.title_enc) });
  const order = new Map(lists.map((l, i) => [l.id, i]));
  const rawCards = [...(snap.cards || [])].sort((a, b) =>
    (order.get(a.list_id ?? -1) ?? 0) - (order.get(b.list_id ?? -1) ?? 0) ||
    byRank(ranked(a), ranked(b))
  );
  const cards: IndexCard[] = [];
  for (const c of rawCards) {
    cards.push({
      id: c.id,
      list: c.list_id ?? 0,
      kind: String(c.kind || "question"),
      desc: await dec(c.description_enc),
      alias: await dec(c.alias_enc),
    });
  }
  return { lists, cards };
}

// What GET /api/boards/{id}/comments returns, per comment.
interface CommentRow { id: number; card_id: number; payload_enc: string }

async function decryptComments(dk: DataKey, rows: CommentRow[]): Promise<IndexComment[]> {
  const out: IndexComment[] = [];
  for (const row of rows) {
    if (!row.card_id || !row.payload_enc) continue;
    // decodeCommentPayload: a comment carrying images stores {text, refs} —
    // search wants the words, not the envelope.
    try {
      const text = decodeCommentPayload(await xyCrypto.decField(dk, row.payload_enc)).text;
      if (text) out.push({ card: row.card_id, id: row.id, text });
    } catch (_) {}
  }
  return out;
}

// indexBoard rebuilds one board's index from a snapshot, comments and all.
// Returns the number of cards indexed.
export async function indexBoard(boardId: number, dk: DataKey, name: string, snap: BoardSnapshot): Promise<number> {
  const { lists, cards } = await decrypt(dk, snap);
  await putCards(boardId, name, lists, cards);
  await refreshComments(boardId, dk);
  return cards.length;
}

// ---- прогрев ----

export interface BoardRef { id: number; name: string }

// prewarm fills the Mirror and the Search Index for every board this device can
// unlock — the only way coverage stops depending on which boards were opened.
// Online-only, and it skips locked boards silently: without a key there is
// nothing to read.
export async function prewarm(boards: BoardRef[], progress: (done: number, total: number) => void): Promise<number> {
  let done = 0, indexed = 0;
  for (const b of boards) {
    progress(done, boards.length);
    done++;
    const dk = await xyCrypto.loadCachedDK(b.id).catch(() => null);
    if (!dk) continue;
    try {
      // A board with queued edits keeps the mirror it has: the server snapshot
      // knows nothing of them, and overwriting it would lose work the outbox has
      // not flushed yet (unlock.ts refuses the same overwrite for the same
      // reason). Its index is then built from the mirror, pending edits and all.
      const pending = await xySync.pendingCountForBoard(b.id);
      let snap: BoardSnapshot | null | undefined;
      if (pending === 0) {
        snap = (await xyApp.fetchJSON(`/api/boards/${b.id}`)) as BoardSnapshot;
        await xySync.saveSnapshot(b.id, snap);
      } else {
        snap = await xySync.loadSnapshot(b.id);
      }
      if (!snap) continue;
      await indexBoard(b.id, dk, b.name || snap.name || "", snap);
      indexed++;
    } catch (_) { /* one unreachable board must not stop the rest */ }
  }
  progress(done, boards.length);
  return indexed;
}

// ---- searching ----

// What cardTitle needs of a card — the board's own BoardCard fits, so both
// callers share this rather than keeping two spellings of the same rule.
export interface TitleableCard { kind: string; desc: string; alias: string | null }

// cardTitle is what a row calls the card: its Alias when it has one, else the
// same preview the board shows. `mode` is the reader's card-title preference
// where the caller knows it; an index built by прогрев does not.
export function cardTitle(c: TitleableCard, mode: string | null = null, blank = "(пусто)"): string {
  const alias = (c.alias || "").trim();
  if (alias) return alias;
  return xyChgk.previewText(c.kind, c.desc, mode).trim().split("\n")[0] || blank;
}

// One board's text folded for searching. Folding is most of the cost of a match,
// and a search runs on every keystroke over every card of every board, so each
// board folds once per page load and the result rides on the index object it
// came from.
interface FoldedCard {
  card: IndexCard;
  desc: Haystack;
  alias: Haystack;
  comments: Array<{ comment: IndexComment; text: Haystack }>;
}
interface FoldedBoard { listName: Map<number, string>; cards: FoldedCard[] }

const foldCache = new WeakMap<BoardIndex, FoldedBoard>();

function folded(index: BoardIndex): FoldedBoard {
  const hit = foldCache.get(index);
  if (hit) return hit;
  const byCard = new Map<number, IndexComment[]>();
  for (const c of index.comments) {
    const arr = byCard.get(c.card);
    if (arr) arr.push(c); else byCard.set(c.card, [c]);
  }
  const out: FoldedBoard = {
    listName: new Map(index.lists.map((l) => [l.id, l.title])),
    cards: index.cards.map((card) => ({
      card,
      desc: xyFind.prepare(card.desc),
      alias: xyFind.prepare(card.alias),
      comments: (byCard.get(card.id) || []).map((comment) => ({ comment, text: xyFind.prepare(comment.text) })),
    })),
  };
  foldCache.set(index, out);
  return out;
}

// search walks the given indexes in the order they are handed in (the board list
// is ordered by last visit) and reports one Hit per matching card.
export function search(
  indexes: ReadonlyArray<{ board: number; index: BoardIndex }>,
  query: string,
  limit: number,
  commentLimit = limit,
): SearchResult {
  const questions: Hit[] = [];
  const comments: Hit[] = [];
  let questionTotal = 0, commentTotal = 0;
  for (const { board, index } of indexes) {
    const { listName, cards } = folded(index);
    for (const card of cards) {
      const where = {
        board,
        boardName: index.name,
        list: listName.get(card.card.list) || "",
        card: card.card.id,
        title: cardTitle(card.card),
      };
      // The question: one hit per card, whether the needle landed in its 4s or
      // in its alias.
      const inDesc = xyFind.searchIn(card.desc, query);
      const inAlias = xyFind.searchIn(card.alias, query);
      if (inDesc.length || inAlias.length) {
        questionTotal++;
        if (questions.length < limit) {
          // The snippet is drawn around the field the needle first landed in;
          // matches in the OTHER field are elsewhere by definition, so they join
          // the count of what the window cannot show.
          const [text, spans, elsewhere] = inDesc.length
            ? [card.card.desc, inDesc, inAlias.length]
            : [card.card.alias, inAlias, 0];
          const snip = xyFind.snippet(text, spans);
          questions.push({ ...where, snippet: snip, more: snip.hidden + elsewhere });
        }
      }
      // The discussion: one hit per COMMENT, since each is its own remark and
      // its own link.
      for (const c of card.comments) {
        const spans = xyFind.searchIn(c.text, query);
        if (!spans.length) continue;
        commentTotal++;
        if (comments.length >= commentLimit) continue;
        const snip = xyFind.snippet(c.comment.text, spans);
        comments.push({ ...where, snippet: snip, more: snip.hidden, comment: c.comment.id });
      }
    }
  }
  return { questions, comments, questionTotal, commentTotal };
}

export const xySearchIndex = {
  load, all, forget, putCards, refreshComments, decrypt, indexBoard, prewarm, search, cardTitle,
};
