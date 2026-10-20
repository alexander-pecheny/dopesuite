// tgexport.ts — the telegram publisher's dialog: the export that posts instead
// of downloading. The dialog asks for the bot and the channel (the server finds
// its discussion group), then watches /api/export/telegram, which answers with a
// stream of lines rather than a file — resolving a channel named by @username is
// a conversation the reader has to take part in, inside Telegram.
//
// What they type is remembered on this device (IndexedDB, store.ts): the bot
// once, the channel per board, and beside it the id Telegram resolved, so the
// second export of the same package skips the conversation entirely.

import { xyApp } from "./app.js";
import { xyStore } from "./store.js";
import { modal } from "./modal.js";
import S from "./i18nstrings.js";

const { byId, errMsg } = xyApp;

// TelegramLine is one line of the endpoint's stream. An empty one is the
// keepalive that keeps the proxies in between from calling the wait a stall.
interface TelegramLine {
  note?: string;
  error?: string;
  done?: boolean;
  channel?: string;
}

export interface TelegramExportDeps {
  // boardId says which board's target is being remembered.
  boardId: () => number;
  // post is the request itself, injected so the tests need no network.
  post?: (body: FormData) => Promise<Response>;
}

export function createTelegramExport(deps: TelegramExportDeps) {
  const tgModal = modal("tgExport");
  const post = deps.post ?? ((body: FormData) =>
    fetch("/api/export/telegram", { method: "POST", credentials: "same-origin", body }));

  function field(id: string): HTMLInputElement { return byId<HTMLInputElement>(id); }

  // The log opens on a placeholder, because a dialog that answers a press with
  // nothing reads as broken; the stream's first line takes its place.
  let placeholder = false;
  function log(line: string): void {
    const msg = byId("tgExportMessage");
    msg.textContent = placeholder || !msg.textContent ? line : `${msg.textContent}\n${line}`;
    placeholder = false;
  }

  // The package to post comes in already assembled: the export dialog holds the
  // list, and it is closed by the time this one opens.
  let body: FormData | null = null;

  async function open(pack: FormData): Promise<void> {
    body = pack;
    const saved = await xyStore.getTgTarget(deps.boardId());
    field("tgExportToken").value = (await xyStore.getTgBot()) ?? "";
    field("tgExportChannel").value = saved?.channel ?? "";
    byId("tgExportMessage").textContent = "";
    tgModal.open();
  }

  async function run(): Promise<void> {
    const token = field("tgExportToken").value.trim();
    const channel = field("tgExportChannel").value.trim();
    if (!token || !channel || !body) return;
    const boardId = deps.boardId();
    await addTarget(body, boardId, token, channel);

    const btn = byId<HTMLButtonElement>("tgExportRun");
    btn.disabled = true;
    byId("tgExportMessage").textContent = S.board.tgexport.running();
    placeholder = true;
    try {
      const res = await post(body);
      if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
      await read(res, boardId, channel);
    } catch (err) {
      log(errMsg(err));
    } finally {
      btn.disabled = false;
    }
  }

  // addTarget puts the bot and its destination into the request and remembers
  // them for the next export from this board.
  async function addTarget(form: FormData, boardId: number, token: string, channel: string): Promise<void> {
    // The id Telegram resolved last time stands in for the same name typed
    // again — that, and not the name, is what spares the reader the dialogue.
    const saved = await xyStore.getTgTarget(boardId);
    const same = saved?.channel === channel;
    form.append("token", token);
    form.append("channel", (same && saved?.channelId) || channel);
    await xyStore.putTgBot(token);
    // A renamed channel keeps no id: that belonged to the old one. It comes
    // back on the last line of the stream, once Telegram has said what it is.
    await xyStore.putTgTarget(boardId, same ? { channel, channelId: saved?.channelId } : { channel });
  }

  // read walks the stream. A dropped connection ends it without a last line,
  // and that is worth saying out loud: the questions posted so far are posted.
  async function read(res: Response, boardId: number, channel: string): Promise<void> {
    const reader = res.body?.getReader();
    if (!reader) return;
    const decoder = new TextDecoder();
    let buffer = "";
    let finished = false;
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let cut = buffer.indexOf("\n");
      for (; cut >= 0; cut = buffer.indexOf("\n")) {
        const raw = buffer.slice(0, cut).trim();
        buffer = buffer.slice(cut + 1);
        if (!raw) continue;
        const line = JSON.parse(raw) as TelegramLine;
        if (line.note) log(line.note);
        if (line.error) { log(line.error); finished = true; }
        if (line.done) {
          await xyStore.putTgTarget(boardId, { channel, channelId: line.channel });
          log(S.board.tgexport.done());
          finished = true;
        }
      }
    }
    if (!finished) log(S.board.tgexport.interrupted());
  }

  byId("tgExportForm").addEventListener("submit", (e) => { e.preventDefault(); void run(); });

  return { open, run };
}
