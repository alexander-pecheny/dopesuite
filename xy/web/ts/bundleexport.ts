// bundleexport.ts — «Скачать доску (.zip)»: the Board Bundle export (ADR-0013).
// Everything the board holds — lists, cards, labels, sessions, the full
// timeline, attachments — decrypted under the key this client already has and
// packed into one plaintext zip the import page of any xy instance can read.

import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import type { DataKey } from "./crypto.js";
import { attachmentPath, BOARD_JSON, BUNDLE_FORMAT } from "./bundle.js";
import type { Bundle, BundleAttachment, BundleEvent } from "./bundle.js";
import { zipWrite } from "./zip.js";
import type { ZipEntry } from "./zip.js";
import type { Board, BoardPanel, PanelShell } from "./panels.js";

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

async function buildExport(board: Board, log: (line: string) => void): Promise<{ name: string; blob: Blob }> {
  const dk: DataKey = board.dk();
  const state = board.state;
  const dec = (b64: string): Promise<string> => xyCrypto.decField(dk, b64);

  log("Собираю данные доски…");
  const members = (await fetchJSON(`/api/boards/${board.id}/members`)) as MemberRow[];
  const rawEvents = (await fetchJSON(`/api/boards/${board.id}/timeline`)) as EventRow[];
  const rawAtts = (await fetchJSON(`/api/boards/${board.id}/attachments`)) as AttachmentRow[];

  const timeline: BundleEvent[] = [];
  for (const e of rawEvents) {
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
    if (timeline.length % 200 === 0) log(`Расшифровываю историю… (${timeline.length}/${rawEvents.length})`);
  }

  const attachments: BundleAttachment[] = [];
  const entries: ZipEntry[] = [];
  for (const a of rawAtts) {
    const filename = await dec(a.filename_enc);
    log(`Скачиваю вложения… (${attachments.length + 1}/${rawAtts.length})`);
    const res = await fetch(`/api/attachments/${a.id}`, { credentials: "same-origin" });
    if (!res.ok) throw new Error(`вложение «${filename}»: ${res.status}`);
    const plain = await xyCrypto.decBytes(dk, new Uint8Array(await res.arrayBuffer()));
    const path = attachmentPath(a.id, filename);
    attachments.push({
      id: a.id, card_id: a.card_id, filename, mime: a.mime,
      size: a.size, lossless: !!a.lossless, is_excerpt: !!a.is_excerpt, path,
    });
    entries.push({ name: path, data: plain });
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
  entries.unshift({ name: BOARD_JSON, data: new TextEncoder().encode(JSON.stringify(bundle, null, 1)) });

  log("Собираю архив…");
  const zipped = await zipWrite(entries, (name) => name === BOARD_JSON);
  const stem = (state.name || "board").replace(/[/\\:*?"<>|]/g, "_").trim() || "board";
  return { name: `${stem}.xyboard.zip`, blob: new Blob([zipped], { type: "application/zip" }) };
}

export function createBundleExportPanel(board: Board, shell: PanelShell): BoardPanel {
  return {
    id: "bundle-export", menu: "board", icon: "file-down",
    label: "Скачать доску (.zip)",
    title: "Скачать всю доску одним архивом — для переноса на другой сервер xy",
    open() {
      const status = el("p", { class: "hint" }, "");
      const btn = el("button", { class: "btn btn-primary", type: "button" }, "Скачать .zip") as HTMLButtonElement;
      const body = el("div", {},
        el("p", { class: "hint" },
          "Архив содержит всю доску: списки, карточки, метки, тесты, комментарии, ",
          "историю правок и вложения. Его можно импортировать на другом сервере xy ",
          "(страница «Импорт»)."),
        el("p", { class: "hint hint-danger" },
          "Файл НЕ зашифрован: внутри — расшифрованное содержимое доски. ",
          "Храните его как пароль."),
        btn, status);
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        try {
          const { name, blob } = await buildExport(board, (line) => { status.textContent = line; });
          downloadBlob(blob, name);
          status.textContent = "Готово — архив скачан.";
        } catch (e) {
          status.textContent = "Не получилось: " + errMsg(e);
        } finally {
          btn.disabled = false;
        }
      });
      shell.open({ icon: "file-down", title: "Скачать доску", body, onClose: () => {} });
    },
  };
}
