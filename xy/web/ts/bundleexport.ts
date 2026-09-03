// bundleexport.ts — "Export archive (.zip)": the Bundle export (ADR-0013), and one
// place a live board becomes a Bundle. buildBundle decrypts what the ticked
// Lists reach under the key this client already has; the panel packs that into
// a plaintext zip, and a cross-board Transfer hands the same pair straight to
// applyBundle (ADR-0014) without a file in between.
//
// Attachment bytes come back through a lazy `bytesOf` rather than in the
// Bundle: a board's worth of handouts must not sit on the heap just because
// something downstream might want them.

import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import type { DataKey } from "./crypto.js";
import { attachmentPath, BOARD_JSON, BUNDLE_FORMAT, sliceBundle, unitsOf } from "./bundle.js";
import type { Bundle, BundleAttachment, BundleEvent, BundleUnit } from "./bundle.js";
import type { AttachmentBytes } from "./bundleapply.js";
import { zipWrite } from "./zip.js";
import type { ZipEntry } from "./zip.js";
import type { Board, BoardPanel, PanelShell } from "./panels.js";
import S from "./i18nstrings.js";

const { fetchJSON, downloadBlob, el, errMsg } = xyApp;

interface MemberRow {
  user_id: number;
  role: string;
  username: string | null;
}
interface EventRow {
  id: number;
  card_id?: number;
  session_id?: number;
  type: string;
  author_username?: string;
  created_at: string;
  edited_at?: string;
  is_excerpt: boolean;
  reply_to_id?: number;
  payload_enc: string;
}
interface AttachmentRow {
  id: number;
  card_id: number;
  filename_enc: string;
  mime: string;
  size: number;
  lossless: boolean;
  is_excerpt: boolean;
}

// buildBundle turns the live board into a Bundle holding the ticked Lists and
// everything they reach (sliceBundle's rule). listIds null means every List.
export async function buildBundle(
  board: Board,
  listIds: number[] | null,
  log: (line: string) => void,
): Promise<{ bundle: Bundle; bytesOf: AttachmentBytes }> {
  const dk: DataKey = board.dk();
  const state = board.state;
  const dec = (b64: string): Promise<string> => xyCrypto.decField(dk, b64);

  log(S.import.export.collecting());
  const members = (await fetchJSON(`/api/boards/${board.id}/members`)) as MemberRow[];
  const rawEvents = (await fetchJSON(`/api/boards/${board.id}/timeline`)) as EventRow[];
  const rawAtts = (await fetchJSON(`/api/boards/${board.id}/attachments`)) as AttachmentRow[];

  // Decrypt only what the ticked Lists reach: sliceBundle would drop the rest
  // anyway, and a whole board's history is a lot of work to throw away.
  const kept = listIds == null ? null : new Set(state.cards.filter((c) => listIds.includes(c.listId)).map((c) => c.id));
  const mine = (cardId: number | null | undefined): boolean => kept == null || (cardId != null && kept.has(cardId));

  const timeline: BundleEvent[] = [];
  for (const e of rawEvents.filter((e) => mine(e.card_id) || e.card_id == null)) {
    timeline.push({
      id: e.id,
      card_id: e.card_id ?? null,
      session_id: e.session_id ?? null,
      type: e.type,
      author: e.author_username ?? null,
      created_at: e.created_at,
      edited_at: e.edited_at ?? null,
      is_excerpt: !!e.is_excerpt,
      reply_to_id: e.reply_to_id ?? null,
      payload: await dec(e.payload_enc),
    });
    if (timeline.length % 200 === 0) log(S.import.export.decrypting(String(timeline.length)));
  }

  const attachments: BundleAttachment[] = [];
  for (const a of rawAtts.filter((a) => mine(a.card_id))) {
    const filename = await dec(a.filename_enc);
    attachments.push({
      id: a.id, card_id: a.card_id, filename, mime: a.mime,
      size: a.size, lossless: !!a.lossless, is_excerpt: !!a.is_excerpt,
      path: attachmentPath(a.id, filename),
    });
  }

  const bundle: Bundle = {
    format: BUNDLE_FORMAT,
    exported_at: new Date().toISOString(),
    board: { name: state.name },
    members: members.filter((m) => m.username).map((m) => ({ username: m.username!, role: m.role })),
    lists: state.lists.map((l) => ({ id: l.id, type: l.type, title: l.title, rank: l.rank, group_id: l.groupId })),
    groups: state.groups.map((g) => ({ id: g.id, name: g.name })),
    cards: state.cards.map((c) => ({
      id: c.id, list_id: c.listId, kind: c.kind, description: c.desc, rank: c.rank,
      handout_meta: c.handoutMeta, alias: c.alias, created_at: c.createdAt,
    })),
    labels: state.labels.map((l) => ({ id: l.id, name: l.name, color: l.color })),
    sessions: state.sessions.map((s) => ({ id: s.id, meta: s.meta, created_at: s.createdAt })),
    card_labels: state.cardLabels.map((a) => ({ card_id: a.cardId, label_id: a.labelId, session_id: a.sessionId })),
    card_sessions: state.cardSessions.map((p) => ({ card_id: p.cardId, session_id: p.sessionId })),
    tour_testers: state.tourTesters.map((t) => ({ list_id: t.listId, group_id: t.groupId, session_id: t.sessionId })),
    timeline,
    attachments,
  };

  const bytesOf: AttachmentBytes = async (a) => {
    const res = await fetch(`/api/attachments/${a.id}`, { credentials: "same-origin" });
    if (!res.ok) throw new Error(S.import.export.attachFailed(a.filename, String(res.status)));
    return await xyCrypto.decBytes(dk, new Uint8Array(await res.arrayBuffer()));
  };
  return {
    bundle: listIds ? sliceBundle(bundle, listIds) : bundle,
    bytesOf,
  };
}

// zipBundle materialises a Bundle as the file that travels: board.json stored
// uncompressed so `unzip -p` reads it, every attachment beside it.
async function zipBundle(
  bundle: Bundle,
  bytesOf: AttachmentBytes,
  stem: string,
  log: (line: string) => void,
): Promise<{ name: string; blob: Blob }> {
  const entries: ZipEntry[] = [];
  let done = 0;
  for (const a of bundle.attachments) {
    log(S.import.export.downloading(String(++done), String(bundle.attachments.length)));
    const data = await bytesOf(a);
    if (data) entries.push({ name: a.path, data });
  }
  entries.unshift({ name: BOARD_JSON, data: new TextEncoder().encode(JSON.stringify(bundle, null, 1)) });
  log(S.import.export.zipping());
  const zipped = await zipWrite(entries, (name) => name === BOARD_JSON);
  const safe = stem.replace(/[/\\:*?"<>|]/g, "_").trim() || "board";
  return { name: `${safe}.xyboard.zip`, blob: new Blob([zipped], { type: "application/zip" }) };
}

// tickList builds the picker every Bundle panel shows: one row per unit, a
// List Group as one indivisible row because its Lists share a numbering
// sequence and travel as one block.
export function tickList(
  units: BundleUnit[],
  note?: (u: BundleUnit) => string,
): { node: HTMLElement; picked: () => BundleUnit[]; toggleAll: () => void } {
  const boxes = units.map((u) => {
    const box = el("input", { type: "checkbox", checked: "checked" }) as HTMLInputElement;
    box.checked = true;
    const warn = note ? note(u) : "";
    const row = el("label", { class: "attach-lossless" }, box, el("span", {},
      u.group ? `🔗 ${u.title}` : u.title,
      ...(warn ? [el("span", { class: "hint hint-danger" }, ` ⚠ ${warn}`)] : []),
    ));
    return { u, box, row };
  });
  const node = el("div", { class: "u-col u-gap-sm" }, ...boxes.map((b) => b.row));
  return {
    node,
    picked: () => boxes.filter((b) => b.box.checked).map((b) => b.u),
    toggleAll: () => {
      const on = boxes.some((b) => !b.box.checked);
      for (const b of boxes) b.box.checked = on;
    },
  };
}

export function createBundleExportPanel(board: Board, shell: PanelShell): BoardPanel {
  return {
    id: "bundle-export", menu: "board", icon: "package",
    label: S.import.export.label(),
    title: S.import.export.menuTitle(),
    open() {
      const state = board.state;
      const units = unitsOf(
        state.lists.map((l) => ({ id: l.id, title: l.title, rank: l.rank, group_id: l.groupId })),
        state.groups,
      );
      const ticks = tickList(units);
      const status = el("p", { class: "hint" }, "");
      const btn = el("button", { class: "btn btn-primary", type: "button" }, S.import.export.submit()) as HTMLButtonElement;
      const all = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, S.import.export.selectAll()) as HTMLButtonElement;
      const body = el("div", { class: "u-col u-gap-sm" },
        el("p", { class: "hint" }, S.import.export.hintBody()),
        el("p", { class: "hint hint-danger" }, S.import.export.notEncrypted()),
        ticks.node,
        el("div", { class: "u-row u-gap-sm u-wrap" }, all, btn),
        status);
      all.addEventListener("click", () => ticks.toggleAll());
      btn.addEventListener("click", async () => {
        const picked = ticks.picked();
        if (!picked.length) {
          status.textContent = S.import.export.nonePicked();
          return;
        }
        btn.disabled = true;
        const log = (line: string): void => { status.textContent = line; };
        try {
          const whole = picked.length === units.length;
          const listIds = picked.flatMap((u) => u.listIds);
          const { bundle, bytesOf } = await buildBundle(board, listIds, log);
          const stem = whole || picked.length > 1 ? state.name : `${state.name} — ${picked[0].title}`;
          const { name, blob } = await zipBundle(bundle, bytesOf, stem, log);
          downloadBlob(blob, name);
          status.textContent = S.import.export.downloaded();
        } catch (e) {
          status.textContent = S.import.export.failed(errMsg(e));
        } finally {
          btn.disabled = false;
        }
      });
      shell.open({ icon: "package", title: S.import.export.modalTitle(), body, onClose: () => {} });
    },
  };
}
