// bundleimport.ts — import a Board Bundle zip into a new encrypted board
// (ADR-0013). The mirror of bundleexport.ts: the plaintext archive is parsed
// in the browser, every field re-encrypted under a fresh board key, every id
// remapped. Import never merges — it always creates a new board; any failure
// deletes the half-created board and reports.

import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { BOARD_JSON, contentBytes, parseBundle } from "./bundle.js";
import type { Bundle, BundleEvent } from "./bundle.js";
import { zipRead } from "./zip.js";

const { fetchJSON, jpost, jput, errMsg } = xyApp;

// One import batch stays well under the server's per-request event cap.
const EVENT_CHUNK = 400;

interface StorageInfo {
  used_bytes: number;
  quota_bytes: number;
  unlimited?: boolean;
}

async function checkQuota(bundle: Bundle): Promise<void> {
  const s = (await fetchJSON("/api/auth/storage")) as StorageInfo;
  if (s.unlimited) return;
  // Ciphertext sizes match the plaintext's within an envelope's few dozen
  // bytes per field; 10% headroom covers that.
  const need = Math.ceil(contentBytes(bundle) * 1.1);
  const left = s.quota_bytes - s.used_bytes;
  if (need > left) {
    const mb = (n: number): string => (n / (1 << 20)).toFixed(1);
    throw new Error(`доска из архива (~${mb(need)} МБ) не помещается в остаток хранилища (${mb(Math.max(left, 0))} МБ)`);
  }
}

export async function importBundle(file: File, name: string, pass: string, log: (line: string) => void): Promise<{ id: number; summary: string }> {
  log("Читаю архив…");
  const entries = await zipRead(new Uint8Array(await file.arrayBuffer()));
  const files = new Map(entries.map((e) => [e.name, e.data]));
  const boardJson = files.get(BOARD_JSON);
  if (!boardJson) throw new Error(`в архиве нет ${BOARD_JSON} — это не экспорт доски xy`);
  const bundle = parseBundle(new TextDecoder().decode(boardJson));
  await checkQuota(bundle);

  log("Создаю доску…");
  const { keymeta, dk } = await xyCrypto.createBoardKeys(pass);
  const created = (await jpost("/api/boards", { ...keymeta, name: name || bundle.board.name })) as { id: number };
  const boardId = created.id;
  const enc = (s: string): Promise<string> => xyCrypto.encField(dk, s);

  try {
    // Every id in the bundle is the source instance's; each create records the
    // fresh one, and everything downstream resolves through these maps.
    const listMap = new Map<number, number>();
    const labelMap = new Map<number, number>();
    const sessionMap = new Map<number, number>();
    const cardMap = new Map<number, number>();

    const byRank = <T extends { rank: string }>(rows: T[]): T[] => [...rows].sort((a, b) => a.rank < b.rank ? -1 : a.rank > b.rank ? 1 : 0);

    for (const l of byRank(bundle.lists)) {
      const res = (await jpost(`/api/boards/${boardId}/lists`, {
        title_enc: await enc(l.title), rank: l.rank, type: l.type,
      })) as { id: number };
      listMap.set(l.id, res.id);
    }
    for (const g of bundle.groups) {
      const members = byRank(bundle.lists.filter((l) => l.group_id === g.id)).map((l) => listMap.get(l.id)!);
      if (members.length < 2) continue; // a group needs two lists; a stray one stays ungrouped
      await jpost(`/api/boards/${boardId}/list-groups`, { name_enc: await enc(g.name), list_ids: members });
    }
    for (const l of bundle.labels) {
      const res = (await jpost(`/api/boards/${boardId}/labels`, {
        name_enc: await enc(l.name), color_enc: await enc(l.color),
      })) as { id: number };
      labelMap.set(l.id, res.id);
    }
    for (const s of bundle.sessions) {
      const res = (await jpost(`/api/boards/${boardId}/sessions`, { meta_enc: await enc(s.meta) })) as { id: number };
      sessionMap.set(s.id, res.id);
    }

    let done = 0;
    for (const c of bundle.cards) {
      const body: Record<string, unknown> = { description_enc: await enc(c.description), rank: c.rank, kind: c.kind };
      if (c.handout_meta) body.handout_meta_enc = await enc(c.handout_meta);
      if (c.alias) body.alias_enc = await enc(c.alias);
      const res = (await jpost(`/api/lists/${listMap.get(c.list_id)}/cards`, body)) as { id: number };
      cardMap.set(c.id, res.id);
      done++;
      if (done % 20 === 0) log(`Создаю карточки… (${done}/${bundle.cards.length})`);
    }

    // Playings before label assignments: a session-scoped label needs its
    // Playing on record (the server checks).
    const playings = new Map<number, number[]>();
    for (const p of bundle.card_sessions) {
      if (!playings.has(p.card_id)) playings.set(p.card_id, []);
      playings.get(p.card_id)!.push(sessionMap.get(p.session_id)!);
    }
    for (const [cardId, sessionIds] of playings) {
      await jput(`/api/cards/${cardMap.get(cardId)}/sessions`, { session_ids: sessionIds });
    }
    const assignments = new Map<number, { label_id: number; session_id: number | null }[]>();
    for (const a of bundle.card_labels) {
      if (!assignments.has(a.card_id)) assignments.set(a.card_id, []);
      assignments.get(a.card_id)!.push({
        label_id: labelMap.get(a.label_id)!,
        session_id: a.session_id == null ? null : sessionMap.get(a.session_id)!,
      });
    }
    for (const [cardId, labels] of assignments) {
      await jput(`/api/cards/${cardMap.get(cardId)}/labels`, { labels });
    }

    // Tour Declarations, one PUT per tour; a null session_id row is the
    // declared-names-nobody marker, i.e. an empty set.
    const tours = new Map<string, { list_id: number | null; group_id: number | null; session_ids: number[] }>();
    for (const t of bundle.tour_testers) {
      const key = t.list_id != null ? `l${t.list_id}` : `g${t.group_id}`;
      if (!tours.has(key)) {
        tours.set(key, {
          list_id: t.list_id == null ? null : listMap.get(t.list_id)!,
          group_id: null, // group ids are the server's own — resolved below
          session_ids: [],
        });
      }
      if (t.session_id != null) tours.get(key)!.session_ids.push(sessionMap.get(t.session_id)!);
    }
    // Group Declarations need the NEW group ids, which POST list-groups did not
    // return per source id; resolve them through the fresh snapshot.
    if ([...tours.keys()].some((k) => k.startsWith("g"))) {
      const snap = (await fetchJSON(`/api/boards/${boardId}`)) as { lists: { id: number; group_id?: number | null }[] };
      const groupOfList = new Map(snap.lists.map((l) => [l.id, l.group_id ?? null]));
      for (const [key, tour] of tours) {
        if (!key.startsWith("g")) continue;
        const srcGroup = Number(key.slice(1));
        const member = bundle.lists.find((l) => l.group_id === srcGroup);
        tour.group_id = member ? groupOfList.get(listMap.get(member.id)!) ?? null : null;
        if (tour.group_id == null) tours.delete(key); // the group was skipped (see above)
      }
    }
    for (const tour of tours.values()) {
      await jput(`/api/boards/${boardId}/tour-testers`, tour);
    }

    // Timeline: oldest first so a parent always precedes its replies; batches
    // chain through the returned src→new map.
    const events = [...bundle.timeline].sort((a, b) => a.id - b.id);
    const eventMap = new Map<number, number>();
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
    for (let at = 0; at < events.length; at += EVENT_CHUNK) {
      const chunk = events.slice(at, at + EVENT_CHUNK);
      const wire = [];
      for (const e of chunk) wire.push(await toWire(e));
      const res = (await jpost(`/api/boards/${boardId}/timeline/import`, { events: wire })) as { ids: Record<string, number> };
      for (const [src, id] of Object.entries(res.ids)) eventMap.set(Number(src), id);
      log(`Переношу историю… (${Math.min(at + chunk.length, events.length)}/${events.length})`);
    }

    let attDone = 0;
    for (const a of bundle.attachments) {
      const bytes = files.get(a.path);
      if (!bytes) throw new Error(`в архиве нет файла ${a.path}`);
      const fd = new FormData();
      fd.append("meta", JSON.stringify({
        filename_enc: await enc(a.filename),
        mime: a.mime || "application/octet-stream",
        lossless: !!a.lossless,
        is_excerpt: !!a.is_excerpt,
        // no event_payload_enc: the original attach_add already came with the timeline
      }));
      const cipher = await xyCrypto.encBytes(dk, bytes);
      fd.append("blob", new Blob([cipher], { type: "application/octet-stream" }), "blob");
      const res = await fetch(`/api/cards/${cardMap.get(a.card_id)}/attachments`, { method: "POST", credentials: "same-origin", body: fd });
      if (!res.ok) throw new Error(`вложение «${a.filename}»: ${res.status}`);
      attDone++;
      log(`Загружаю вложения… (${attDone}/${bundle.attachments.length})`);
    }

    await xyCrypto.cacheDK(boardId, dk);
    const summary = `Готово: ${bundle.cards.length} карточек, ${bundle.lists.length} списков, `
      + `${bundle.labels.length} меток, ${bundle.sessions.length} тестов, `
      + `${bundle.timeline.length} событий, ${bundle.attachments.length} вложений.`;
    log(summary);
    return { id: boardId, summary };
  } catch (e) {
    // Never leave a half-imported board behind: it was created seconds ago by
    // this same flow, so deleting it is safe, and a retry starts clean.
    log("Импорт прерван, удаляю недосозданную доску…");
    try {
      await xyApp.jdelete(`/api/boards/${boardId}`);
    } catch { /* the original error matters more */ }
    throw new Error(errMsg(e));
  }
}

export const xyBundleImport = { importBundle };
