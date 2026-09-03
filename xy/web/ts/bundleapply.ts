// bundleapply.ts — the one write path a Transfer takes (ADR-0014). Whatever
// produced the Bundle — a zip someone mailed, a live board this device holds
// the key to, a Trello account — it arrives here as plaintext and leaves as
// ciphertext under the target board's key.
//
// Two targets. A board this call's caller just created takes the Bundle
// verbatim: same ranks, one Label and one Session per row, a replica. An
// existing board takes an APPEND: the lists land after whatever is already
// there, and only Labels and Sessions reconcile onto equivalents that already
// mean the same thing (a Label on name+colour, a Session on its key, ADR-0003).
// Nothing on the target is ever read back and overwritten.
//
// The List is the unit of atomicity: a unit that fails takes its own lists down
// with it and the ones before it stay. The caller reports which is which.

import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xyRank } from "./rank.js";
import { parseSession, serializeSession } from "./sessions.js";
import { bundleUnits } from "./bundle.js";
import type { Bundle, BundleAttachment, BundleEvent, BundleUnit } from "./bundle.js";
import type { DataKey } from "./crypto.js";
import S from "./i18nstrings_ru_gen.js";

const { jpost, jput, jdelete, errMsg } = xyApp;
const { keyBetween } = xyRank;

// One import batch stays well under the server's per-request event cap.
const EVENT_CHUNK = 400;

export interface TargetLabel {
  id: number;
  name: string;
  color: string;
}
export interface TargetSession {
  id: number;
  meta: string;
}

// AppendState is the target board as this apply knows it — its Labels and
// Sessions to reconcile against, and where its lists end. Mutated as the apply
// creates things, so a second unit reuses what the first one made.
export interface AppendState {
  labels: TargetLabel[];
  sessions: TargetSession[];
  lastRank: string | null;
  nextRank?: string | null; // the list the arrivals go before; absent = the end
  sourceName: string; // the Bundle's board name — the origin stamp's other half
}

export interface ApplyTarget {
  boardId: number;
  dk: DataKey;
  append: AppendState | null; // null → a board created for this Bundle
}

// A producer that returns null for an attachment is saying "this one cannot be
// had" — a Trello download that 404s — and the apply carries on without it. A
// producer that must have it (a zip missing its own file) throws instead.
export type AttachmentBytes = (a: BundleAttachment) => Promise<Uint8Array<ArrayBuffer> | null>;

export interface UnitResult {
  title: string;
  cards: number;
  error?: string;
}
export interface ApplyResult {
  units: UnitResult[];
  cards: number;
  attachments: number;
  events: number;
  skipped: string[]; // attachments the producer could not hand over
  failed: boolean;
}

const byRank = <T extends { rank: string }>(rows: T[]): T[] => [...rows].sort((a, b) => a.rank < b.rank ? -1 : a.rank > b.rank ? 1 : 0);

export async function applyBundle(
  bundle: Bundle,
  target: ApplyTarget,
  bytesOf: AttachmentBytes,
  log: (line: string) => void,
): Promise<ApplyResult> {
  const { boardId, dk, append } = target;
  const enc = (s: string): Promise<string> => xyCrypto.encField(dk, s);

  // Every id in the Bundle is its source's; each create records the fresh one,
  // and everything downstream resolves through these maps.
  const listMap = new Map<number, number>();
  const labelMap = new Map<number, number>();
  const sessionMap = new Map<number, number>();
  const cardMap = new Map<number, number>();
  const eventMap = new Map<number, number>();

  const result: ApplyResult = { units: [], cards: 0, attachments: 0, events: 0, skipped: [], failed: false };
  const units = bundleUnits(bundle);

  // reconcileLabel maps a Bundle Label onto the target. Appending, an existing
  // Label with the same name and colour already means this; otherwise — and
  // always on a fresh board — it is created.
  async function reconcileLabel(srcId: number): Promise<number> {
    const known = labelMap.get(srcId);
    if (known != null) return known;
    const src = bundle.labels.find((l) => l.id === srcId)!;
    const match = append && append.labels.find((t) => t.name === src.name && t.color === src.color);
    if (match) {
      labelMap.set(srcId, match.id);
      return match.id;
    }
    const res = (await jpost(`/api/boards/${boardId}/labels`, {
      name_enc: await enc(src.name), color_enc: await enc(src.color),
    })) as { id: number };
    if (append) append.labels.push({ id: res.id, name: src.name, color: src.color });
    labelMap.set(srcId, res.id);
    return res.id;
  }

  // reconcileSession maps a Bundle Session onto the target, matched on the
  // `key` that means "the same sitting" (ADR-0003). A copy carries an origin
  // stamp so the Tests panel can say whose sitting it was and when it was
  // taken; a board created for this Bundle is a replica, not a copy, so it
  // takes the meta verbatim. Returns the new session's id, and true when this
  // call is what created it.
  async function reconcileSession(srcId: number): Promise<{ id: number; fresh: boolean }> {
    const known = sessionMap.get(srcId);
    if (known != null) return { id: known, fresh: false };
    const src = bundle.sessions.find((s) => s.id === srcId)!;
    let meta = src.meta;
    if (append) {
      const parsed = parseSession(src.meta);
      if (parsed.key) {
        const found = append.sessions.find((s) => parseSession(s.meta).key === parsed.key);
        if (found) {
          sessionMap.set(srcId, found.id);
          return { id: found.id, fresh: false };
        }
      }
      meta = serializeSession({
        ...parsed,
        origin: parsed.origin || { board: append.sourceName, at: bundle.exported_at.slice(0, 10) },
      });
    }
    const res = (await jpost(`/api/boards/${boardId}/sessions`, { meta_enc: await enc(meta) })) as { id: number };
    if (append) append.sessions.push({ id: res.id, meta });
    sessionMap.set(srcId, res.id);
    return { id: res.id, fresh: true };
  }

  // nextListRank: appending, the Bundle's own ranks mean nothing on the target,
  // so each list takes the next slot after the last one there.
  function nextListRank(own: string): string {
    if (!append) return own;
    const rank = keyBetween(append.lastRank, append.nextRank ?? null);
    append.lastRank = rank;
    return rank;
  }

  async function importEvents(events: BundleEvent[], what: string): Promise<void> {
    if (!events.length) return;
    const ordered = [...events].sort((a, b) => a.id - b.id);
    const toWire = async (e: BundleEvent): Promise<Record<string, unknown>> => {
      const wire: Record<string, unknown> = {
        src_id: e.id,
        card_id: e.card_id == null ? null : cardMap.get(e.card_id),
        session_id: e.session_id == null ? null : sessionMap.get(e.session_id),
        type: e.type,
        author_username: e.author || "",
        created_at: e.created_at,
        edited_at: e.edited_at || "",
        is_excerpt: !!e.is_excerpt,
        payload_enc: await enc(e.payload),
      };
      if (e.reply_to_id != null) {
        const mapped = eventMap.get(e.reply_to_id);
        if (mapped != null) wire.reply_to_id = mapped;
        else wire.reply_to_src_id = e.reply_to_id;
      }
      return wire;
    };
    for (let at = 0; at < ordered.length; at += EVENT_CHUNK) {
      const chunk = ordered.slice(at, at + EVENT_CHUNK);
      const wire = [];
      for (const e of chunk) wire.push(await toWire(e));
      const res = (await jpost(`/api/boards/${boardId}/timeline/import`, { events: wire })) as { ids: Record<string, number> };
      for (const [src, id] of Object.entries(res.ids)) eventMap.set(Number(src), id);
      result.events += chunk.length;
      log(S.import.apply.history(what, String(Math.min(at + chunk.length, ordered.length)), String(ordered.length)));
    }
  }

  async function applyUnit(unit: BundleUnit): Promise<number> {
    const made: number[] = []; // the lists this unit created — what a rollback takes
    try {
      const lists = byRank(bundle.lists.filter((l) => unit.listIds.includes(l.id)));
      for (const l of lists) {
        const res = (await jpost(`/api/boards/${boardId}/lists`, {
          title_enc: await enc(l.title), rank: nextListRank(l.rank), type: l.type,
        })) as { id: number };
        listMap.set(l.id, res.id);
        made.push(res.id);
      }

      // The group before the Declarations: folding lists into one wipes their
      // per-list tour_testers, which is exactly what a group Declaration replaces.
      let groupId: number | null = null;
      if (unit.group && made.length >= 2) {
        const g = bundle.groups.find((x) => x.id === lists[0].group_id);
        const res = (await jpost(`/api/boards/${boardId}/list-groups`, {
          name_enc: await enc(g ? g.name : unit.title), list_ids: made,
        })) as { id: number };
        groupId = res.id;
      }

      const cards = bundle.cards.filter((c) => unit.listIds.includes(c.list_id));
      let done = 0;
      for (const c of cards) {
        const body: Record<string, unknown> = { description_enc: await enc(c.description), rank: c.rank, kind: c.kind };
        if (c.handout_meta) body.handout_meta_enc = await enc(c.handout_meta);
        if (c.alias) body.alias_enc = await enc(c.alias);
        const res = (await jpost(`/api/lists/${listMap.get(c.list_id)}/cards`, body)) as { id: number };
        cardMap.set(c.id, res.id);
        if (++done % 20 === 0) log(S.import.apply.cards(unit.title, String(done), String(cards.length)));
      }
      const ours = new Set(cards.map((c) => c.id));

      // Sessions this unit is the first to bring across — their own Timeline rides
      // with them rather than with any one card.
      const newSessions: number[] = [];
      const session = async (srcId: number): Promise<number> => {
        const { id, fresh } = await reconcileSession(srcId);
        if (fresh) newSessions.push(srcId);
        return id;
      };

      // Playings before label assignments: a session-scoped label needs its
      // Playing on record (the server checks).
      const playings = new Map<number, number[]>();
      for (const p of bundle.card_sessions) {
        if (!ours.has(p.card_id)) continue;
        if (!playings.has(p.card_id)) playings.set(p.card_id, []);
        playings.get(p.card_id)!.push(await session(p.session_id));
      }
      for (const [cardId, sessionIds] of playings) {
        await jput(`/api/cards/${cardMap.get(cardId)}/sessions`, { session_ids: sessionIds });
      }

      const assignments = new Map<number, Array<{ label_id: number; session_id: number | null }>>();
      for (const a of bundle.card_labels) {
        if (!ours.has(a.card_id)) continue;
        if (!assignments.has(a.card_id)) assignments.set(a.card_id, []);
        assignments.get(a.card_id)!.push({
          label_id: await reconcileLabel(a.label_id),
          session_id: a.session_id == null ? null : await session(a.session_id),
        });
      }
      for (const [cardId, labels] of assignments) {
        await jput(`/api/cards/${cardMap.get(cardId)}/labels`, { labels });
      }

      // Declarations, one PUT per tour; a tour with no session rows is the
      // declared-nobody marker, i.e. an empty set.
      const tours = new Map<string, { list_id: number | null; group_id: number | null; session_ids: number[] }>();
      for (const t of bundle.tour_testers) {
        const mine = t.list_id != null ? unit.listIds.includes(t.list_id) : t.group_id === lists[0]?.group_id;
        if (!mine) continue;
        const key = t.list_id != null ? `l${t.list_id}` : `g${t.group_id}`;
        if (!tours.has(key)) {
          tours.set(key, {
            list_id: t.list_id == null ? null : listMap.get(t.list_id)!,
            group_id: t.list_id == null ? groupId : null,
            session_ids: [],
          });
        }
        if (t.session_id != null) tours.get(key)!.session_ids.push(await session(t.session_id));
      }
      for (const tour of tours.values()) {
        if (tour.list_id == null && tour.group_id == null) continue; // its group was skipped
        await jput(`/api/boards/${boardId}/tour-testers`, tour);
      }

      const keptSessions = new Set(newSessions);
      await importEvents(
        bundle.timeline.filter((e) =>
          (e.card_id != null && ours.has(e.card_id)) ||
          (e.card_id == null && e.session_id != null && keptSessions.has(e.session_id))
        ),
        unit.title,
      );

      const atts = bundle.attachments.filter((a) => ours.has(a.card_id));
      let attDone = 0;
      for (const a of atts) {
        const fd = new FormData();
        fd.append("meta", JSON.stringify({
          filename_enc: await enc(a.filename),
          mime: a.mime || "application/octet-stream",
          lossless: !!a.lossless,
          is_excerpt: !!a.is_excerpt,
          // no event_payload_enc: the original attach_add already came with the timeline
        }));
        const plain = await bytesOf(a);
        if (!plain) {
          result.skipped.push(a.filename);
          continue;
        }
        const cipher = await xyCrypto.encBytes(dk, plain);
        fd.append("blob", new Blob([cipher], { type: "application/octet-stream" }), "blob");
        const res = await fetch(`/api/cards/${cardMap.get(a.card_id)}/attachments`, { method: "POST", credentials: "same-origin", body: fd });
        if (!res.ok) throw new Error(S.import.apply.attachFailed(a.filename, String(res.status)));
        result.attachments++;
        log(S.import.apply.attachments(unit.title, String(++attDone), String(atts.length)));
      }

      result.cards += cards.length;
      return cards.length;
    } catch (e) {
      // The List is the unit: this one goes back the way it came, and the units
      // before it stay. Deleting a list takes its cards with it; a Label or a
      // Session created along the way stays — a finished unit may already be
      // using it, and an unused one is a Tombstone away from gone.
      for (const id of made.reverse()) {
        try { await jdelete(`/api/lists/${id}`); } catch { /* the original error matters more */ }
      }
      throw e;
    }
  }

  for (const unit of units) {
    log(`${unit.title}…`);
    try {
      const cards = await applyUnit(unit);
      result.units.push({ title: unit.title, cards });
    } catch (e) {
      result.units.push({ title: unit.title, cards: 0, error: errMsg(e) });
      result.failed = true;
      return result;
    }
  }
  return result;
}

export const xyBundleApply = { applyBundle };
