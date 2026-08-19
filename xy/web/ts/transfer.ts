// transfer.ts — moving or copying a Card out of its List: on the same board a
// re-rank or a duplicate; to another board a client-side re-encryption of the
// description, the alias, the handout settings, the comments and the
// attachments, with labels reconciled by name and colour and Playings by the
// Test Session's key (ADR-0003). The card editor, «Массовое действие» and
// «Переместить список…» all transfer through here, so a bulk move behaves like
// the card's own move done once per card.
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { xyRank } from "./rank.js";
import { byRank } from "./dragrank.js";
import { parseSession, serializeSession } from "./sessions.js";
import type { BoardKeymeta, DataKey } from "./crypto.js";
import type { BoardCard, BoardLabel, Snapshot } from "./unlock.js";
import type { OpBody } from "./store.js";
import type { CardEvent } from "./timeline.js";
import type { CardDetailState, MoveCtx, MutationVerbs } from "./carddetail.js";

const { fetchJSON, jpost, jput, jdelete } = xyApp;
const { keyBetween } = xyRank;

interface MoveBoardItem { id: number; name?: string; name_enc?: string | null; schema_version?: number }

interface AttachmentDTO {
  id: number;
  filename_enc: string;
  mime: string;
  lossless?: boolean;
  is_excerpt?: boolean;
}

export interface TransferDeps {
  boardId: number;
  getState(): CardDetailState;
  getDK(): DataKey | null;
  verbs: Pick<MutationVerbs, "patch">;
  cardsOf(listId: number): BoardCard[];
  labelById(id: number): BoardLabel | undefined;
  // Asks for another board's passphrase; null cancels. Defaults to prompt().
  askPassphrase?: () => string | null;
}

export interface Transfer {
  ensureDK(bid: number): Promise<DataKey>;
  moveBoardOptions(): Promise<Array<{ id: number; label: string }>>;
  loadMoveBoard(bid: number): Promise<MoveCtx>;
  cardCopyBody(src: BoardCard, rank: string, key: DataKey): Promise<OpBody>;
  copyCardExtras(srcCardId: number, targetDk: DataKey, newCardId: number): Promise<void>;
  reconcileLabels(srcCardId: number, targetBid: number, targetDk: DataKey, ctx: MoveCtx): Promise<Array<{ label_id: number; session_id: number | null }>>;
  reconcilePlayings(srcCardId: number, targetBid: number, targetDk: DataKey, ctx: MoveCtx): Promise<number[]>;
  transferCard(card: BoardCard, targetListId: number, ctx: MoveCtx, remove: boolean, rank?: string): Promise<number>;
}

export function createTransfer(deps: TransferDeps): Transfer {
  const { boardId, verbs, cardsOf, labelById } = deps;
  const st = deps.getState;
  const nowStamp = (): string => new Date().toISOString();
  function mustDK(): DataKey {
    const dk = deps.getDK();
    if (!dk) throw new Error("нет ключа доски");
    return dk;
  }
  const askPassphrase = deps.askPassphrase ?? (() => prompt("Пароль целевой доски:"));

  // ensureDK returns a usable DK for a board, unlocking via passphrase if needed.
  async function ensureDK(bid: number): Promise<DataKey> {
    if (bid === boardId) return mustDK();
    let d = await xyCrypto.loadCachedDK(bid);
    if (d) return d;
    const pass = askPassphrase();
    if (pass == null) throw new Error("отменено");
    const keymeta = (await fetchJSON(`/api/boards/${bid}/keymeta`)) as BoardKeymeta;
    d = await xyCrypto.unlockBoard(pass, keymeta);
    await xyCrypto.cacheDK(bid, d);
    return d;
  }

  // populateMoveBoards fills the board <select> with decrypted board names (the
  // current board first/default), then loads its lists.
  // moveBoardOptions lists every board this move/copy could target, already
  // labelled. Shared by the card's own destination select and the bulk dialog's,
  // so the two can never offer different places.
  async function moveBoardOptions(): Promise<Array<{ id: number; label: string }>> {
    let boards: MoveBoardItem[] = [];
    try { boards = (await fetchJSON("/api/boards")) as MoveBoardItem[]; } catch (_) {}
    // Always offer the current board (so the move UI works — and never prompts for
    // another board's password — even when offline and the board list is unfetched).
    if (!boards.some((b) => b.id === boardId)) boards.unshift({ id: boardId, name_enc: null });
    return await Promise.all(boards.map(async (b) => {
      let label = "доска #" + b.id;
      if (b.id === boardId) label = (st().name || label) + " (эта доска)";
      else if ((b.schema_version ?? 0) >= 2) label = b.name || label; // plaintext name, no key needed
      else {
        try { const cdk = await xyCrypto.loadCachedDK(b.id); if (cdk) label = await xyCrypto.decField(cdk, b.name_enc || ""); }
        catch (_) {}
      }
      return { id: b.id, label };
    }));
  }

  // loadMoveBoard returns a ctx {boardId, dk, lists, cardsByList, labels} for the
  // given board — from in-memory state for the current board, otherwise by
  // fetching + decrypting its snapshot.
  async function loadMoveBoard(bid: number): Promise<MoveCtx> {
    if (bid === boardId) {
      const lists = [...st().lists].sort(byRank).map((l) => ({ id: l.id, title: l.title, rank: l.rank }));
      const cardsByList = new Map<number, Array<{ id: number; rank: string }>>();
      for (const l of lists) cardsByList.set(l.id, cardsOf(l.id).map((c) => ({ id: c.id, rank: c.rank })));
      return {
        boardId: bid, dk: mustDK(), lists, cardsByList, labels: st().labels,
        sessions: st().sessions.map((s) => ({ id: s.id, meta: s.meta })), name: st().name,
      };
    }
    const tdk = await ensureDK(bid);
    const snap = (await fetchJSON(`/api/boards/${bid}`)) as Snapshot;
    const lists = await Promise.all((snap.lists || []).map(async (l) => ({
      id: l.id, rank: l.rank, title: await xyCrypto.decField(tdk, l.title_enc),
    })));
    lists.sort(byRank);
    const cardsByList = new Map<number, Array<{ id: number; rank: string }>>();
    for (const l of lists) {
      cardsByList.set(l.id, (snap.cards || []).filter((c) => c.list_id === l.id).map((c) => ({ id: c.id, rank: c.rank })).sort(byRank));
    }
    const labels = await Promise.all((snap.labels || []).map(async (l) => ({
      id: l.id,
      name: await xyCrypto.decField(tdk, l.name_enc),
      color: await xyCrypto.decField(tdk, l.color_enc),
    })));
    const sessions = await Promise.all((snap.sessions || []).map(async (s) => ({
      id: s.id, meta: await xyCrypto.decField(tdk, s.meta_enc),
    })));
    return {
      boardId: bid, dk: tdk, lists, cardsByList, labels, sessions,
      name: snap.name || "",
    };
  }

  // cardCopyBody builds the create-card payload for a copy: it re-encrypts the
  // description and — when set — the handout-generation settings (field #10,
  // handout_meta_enc) and the alias under `key` (the destination board's data key).
  // kind carries over verbatim. Keeping these here (not in copyCardExtras) means
  // they copy offline too, like the description.
  async function cardCopyBody(src: BoardCard, rank: string, key: DataKey): Promise<OpBody> {
    const body: OpBody = { description_enc: await xyCrypto.encField(key, src.desc), rank, kind: src.kind };
    if (src.handoutMeta) body.handout_meta_enc = await xyCrypto.encField(key, src.handoutMeta);
    if (src.alias) body.alias_enc = await xyCrypto.encField(key, src.alias);
    return body;
  }

  // copyCardExtras carries a source card's comments and attachments onto a freshly
  // created destination card (labels are reconciled separately by the callers). The
  // source card is always on the current board, so its content is read under `dk`
  // and re-encrypted under the destination key `targetDk`. Comments are imported
  // preserving their original author + timestamp (the bulk /timeline/import
  // endpoint); attachments are downloaded, decrypted, re-encrypted and re-uploaded
  // (preserving mime + lossless flag). Copy/move is an online-only operation, so
  // this runs straight against the API (no sync outbox / temp ids).
  async function copyCardExtras(srcCardId: number, targetDk: DataKey, newCardId: number): Promise<void> {
    if (!xySync.isOnline() || !newCardId) return;
    const dk = mustDK();
    // Comments, oldest→newest so the copy keeps the original order, re-encrypted
    // under the destination key but carrying the source author + created_at.
    let events: CardEvent[] = [];
    try { events = (await fetchJSON(`/api/cards/${srcCardId}/timeline`)) as CardEvent[]; } catch (_) { events = []; }
    const comments: Array<Record<string, unknown>> = [];
    for (const ev of events) {
      if (ev.type !== "comment") continue;
      let text: string;
      try { text = await xyCrypto.decField(dk, ev.payload_enc || ""); } catch (_) { continue; }
      comments.push({
        // src ids travel so the server can rebuild threading under fresh ids
        src_id: ev.id,
        reply_to_src_id: ev.reply_to_id != null ? ev.reply_to_id : null,
        author_user_id: ev.author_user_id != null ? ev.author_user_id : null,
        created_at: ev.created_at,
        is_excerpt: !!ev.is_excerpt,
        payload_enc: await xyCrypto.encField(targetDk, text),
      });
    }
    if (comments.length) {
      try { await jpost(`/api/cards/${newCardId}/timeline/import`, { events: comments }); } catch (_) {}
    }
    // Attachments: re-encrypt the ciphertext bytes under the destination key.
    let atts: AttachmentDTO[] = [];
    try { atts = (await fetchJSON(`/api/cards/${srcCardId}/attachments`)) as AttachmentDTO[]; } catch (_) { atts = []; }
    for (const att of atts) {
      let name = "файл";
      try { name = await xyCrypto.decField(dk, att.filename_enc); } catch (_) {}
      let plain: Uint8Array<ArrayBuffer>;
      try {
        const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
        if (!res.ok) continue;
        plain = await xyCrypto.decBytes(dk, new Uint8Array(await res.arrayBuffer()));
      } catch (_) { continue; }
      let recipher: Uint8Array<ArrayBuffer>;
      try { recipher = await xyCrypto.encBytes(targetDk, plain); } catch (_) { continue; }
      const fd = new FormData();
      fd.append("meta", JSON.stringify({
        filename_enc: await xyCrypto.encField(targetDk, name),
        mime: att.mime, lossless: !!att.lossless, is_excerpt: !!att.is_excerpt,
        event_payload_enc: await xyCrypto.encField(targetDk, JSON.stringify({ file: name })),
      }));
      fd.append("blob", new Blob([recipher], { type: "application/octet-stream" }), "blob");
      try { await fetch(`/api/cards/${newCardId}/attachments`, { method: "POST", credentials: "same-origin", body: fd }); } catch (_) {}
    }
  }

  // reconcileLabels maps a source card's label assignments onto the target board,
  // creating whatever is missing there. A label matches on decrypted name+colour.
  // An assignment SCOPED to a playing needs that playing on the target too, so
  // its session is copied across first, matched on `key` — the id of the sitting,
  // which travels verbatim (ADR-0003) because a derived name may render
  // differently for two readers.
  //
  // The session itself has to be copied: boards share no key, so nothing can be
  // referenced across one. The copy carries an `origin` stamp because it diverges
  // from its original the moment either is edited, and its tester list is what
  // «Видели» reads. ctx is the target board's running state, mutated so a batch
  // of copies reuses what it just created.
  async function reconcileLabels(srcCardId: number, targetBid: number, targetDk: DataKey, ctx: MoveCtx): Promise<Array<{ label_id: number; session_id: number | null }>> {
    const out: Array<{ label_id: number; session_id: number | null }> = [];
    for (const a of st().cardLabels.filter((x) => x.cardId === srcCardId)) {
      const sl = labelById(a.labelId);
      if (!sl) continue;
      let match = ctx.labels.find((t) => t.name === sl.name && t.color === sl.color);
      if (!match) {
        const lr = (await jpost(`/api/boards/${targetBid}/labels`, {
          name_enc: await xyCrypto.encField(targetDk, sl.name),
          color_enc: await xyCrypto.encField(targetDk, sl.color),
        })) as { id: number };
        match = { id: lr.id, name: sl.name, color: sl.color };
        ctx.labels.push(match);
      }
      let sessionID: number | null = null;
      if (a.sessionId != null) {
        sessionID = await reconcileSession(a.sessionId, targetBid, targetDk, ctx);
        if (sessionID == null) continue; // a session that predates keys can't be matched
      }
      out.push({ label_id: match.id, session_id: sessionID });
    }
    return out;
  }

  // reconcilePlayings copies the card's Playings — which tests it was played at —
  // and returns the target board's ids for them.
  async function reconcilePlayings(srcCardId: number, targetBid: number, targetDk: DataKey, ctx: MoveCtx): Promise<number[]> {
    const out: number[] = [];
    for (const sid of st().cardSessions.filter((p) => p.cardId === srcCardId).map((p) => p.sessionId)) {
      const id = await reconcileSession(sid, targetBid, targetDk, ctx);
      if (id != null) out.push(id);
    }
    return out;
  }

  async function reconcileSession(srcSessionId: number, targetBid: number, targetDk: DataKey, ctx: MoveCtx): Promise<number | null> {
    const src = st().sessions.find((s) => s.id === srcSessionId);
    if (!src) return null;
    const meta = parseSession(src.meta);
    if (!meta.key) return null;

    const found = ctx.sessions.find((s) => parseSession(s.meta).key === meta.key);
    if (found) return found.id;

    const copy = serializeSession({
      ...meta,
      origin: meta.origin || { board: st().name, at: new Date().toISOString().slice(0, 10) },
    });
    const sr = (await jpost(`/api/boards/${targetBid}/sessions`, {
      meta_enc: await xyCrypto.encField(targetDk, copy),
    })) as { id: number };
    ctx.sessions.push({ id: sr.id, meta: copy });
    return sr.id;
  }

  // transferCard moves or copies ONE card to the end of `ctx`'s target list —
  // the same re-encrypt/reconcile path the card's own move/copy takes, minus the
  // modal. «Массовое действие» drives it once per card, so a bulk move behaves
  // exactly like doing them one at a time, only without the clicking.
  // It appends to ctx.cardsByList as it goes, so a run of cards keeps its order
  // in the destination instead of piling onto one rank.
  async function transferCard(card: BoardCard, targetListId: number, ctx: MoveCtx, remove: boolean, rank?: string): Promise<number> {
    const sameBoard = ctx.boardId === boardId;
    const listCards = ctx.cardsByList.get(targetListId) || [];
    rank ??= keyBetween(listCards.length ? listCards[listCards.length - 1].rank : null, null);
    let newId = card.id;
    if (sameBoard && remove) {
      await verbs.patch("patchCard", `/api/cards/${card.id}`, { list_id: targetListId, rank });
      card.listId = targetListId;
      card.rank = rank;
    } else if (sameBoard) {
      const dk = mustDK();
      const res = (await jpost(`/api/lists/${targetListId}/cards`, await cardCopyBody(card, rank, dk))) as { id: number };
      newId = res.id;
      st().cards.push({ id: res.id, listId: targetListId, kind: card.kind, rank, desc: card.desc, handoutMeta: card.handoutMeta || null, alias: card.alias || null, createdAt: nowStamp() });
      const own = st().cardLabels.filter((a) => a.cardId === card.id);
      const plays = st().cardSessions.filter((p) => p.cardId === card.id).map((p) => p.sessionId);
      if (plays.length) {
        await jput(`/api/cards/${res.id}/sessions`, { session_ids: plays });
        st().cardSessions.push(...plays.map((sessionId) => ({ cardId: res.id, sessionId })));
      }
      if (own.length) {
        await jput(`/api/cards/${res.id}/labels`, { labels: own.map((a) => ({ label_id: a.labelId, session_id: a.sessionId })) });
        st().cardLabels.push(...own.map((a) => ({ ...a, cardId: res.id })));
      }
      await copyCardExtras(card.id, dk, res.id);
    } else {
      const tdk = ctx.dk;
      const res = (await jpost(`/api/lists/${targetListId}/cards`, await cardCopyBody(card, rank, tdk))) as { id: number };
      newId = res.id;
      const plays = await reconcilePlayings(card.id, ctx.boardId, tdk, ctx);
      if (plays.length) await jput(`/api/cards/${res.id}/sessions`, { session_ids: plays });
      const assignments = await reconcileLabels(card.id, ctx.boardId, tdk, ctx);
      if (assignments.length) await jput(`/api/cards/${res.id}/labels`, { labels: assignments });
      await copyCardExtras(card.id, tdk, res.id);
      if (remove) {
        await jdelete(`/api/cards/${card.id}`);
        const s = st();
        s.cards = s.cards.filter((c) => c.id !== card.id);
      }
    }
    listCards.push({ id: newId, rank });
    ctx.cardsByList.set(targetListId, listCards);
    return newId;
  }

  return { ensureDK, moveBoardOptions, loadMoveBoard, cardCopyBody, copyCardExtras, reconcileLabels, reconcilePlayings, transferCard };
}
