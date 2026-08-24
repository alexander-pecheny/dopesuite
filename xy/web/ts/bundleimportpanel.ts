// bundleimportpanel.ts — a Bundle appended to the board that is open. It has no
// menu entry of its own: «Импорт» picks one file, recognises what it is, and
// hands an xy archive here. It lives on the board page rather than on /import
// because appending re-encrypts under this board's key, which only its own
// page holds.
//
// What lands is what the ticks say. Lists append after the ones already here;
// Labels fold onto same-name-same-colour ones; a Session already on the board
// (same sitting, ADR-0003) is reused rather than twinned. A unit that fails
// rolls itself back and the ones before it stay.

import { xyApp } from "./app.js";
import { xySync } from "./sync.js";
import { unitsOf } from "./bundle.js";
import type { Bundle, BundleUnit } from "./bundle.js";
import { applyBundle } from "./bundleapply.js";
import type { AppendState, AttachmentBytes } from "./bundleapply.js";
import { checkQuota, summarize } from "./bundleimport.js";
import { sliceBundle } from "./bundle.js";
import { tickList } from "./bundleexport.js";
import type { Board, BoardPanel, PanelShell } from "./panels.js";

const { el, errMsg } = xyApp;

export interface BundleImport {
  openWith(file: File, bundle: Bundle, bytesOf: AttachmentBytes): void;
}

export function createBundleImport(board: Board, shell: PanelShell): BundleImport {
  return {
    openWith(file, bundle, bytesOf) {
      const status = el("p", { class: "hint" }, "");
      const units = unitsOf(bundle.lists, bundle.groups);
      const here = new Set(board.state.lists.map((l) => l.title));
      const titleOf = (id: number): string | undefined => bundle.lists.find((l) => l.id === id)?.title;
      const clash = (u: BundleUnit): string =>
        u.listIds.some((id) => { const t = titleOf(id); return t !== undefined && here.has(t); })
          ? "такой список уже есть на доске"
          : "";
      const ticks = tickList(units, clash);
      const run = el("button", { class: "btn btn-primary", type: "button" }, "Добавить на доску") as HTMLButtonElement;
      const all = el("button", { class: "btn btn-ghost btn-sm", type: "button" }, "Выбрать все") as HTMLButtonElement;
      const body = el("div", { class: "u-col u-gap-sm" },
        el("p", { class: "hint" },
          `Доска «${bundle.board.name}», выгружена ${bundle.exported_at.slice(0, 10)}. `,
          "Отмеченные списки добавятся к этой доске в конец; метки и тесты подхватятся ",
          "к уже существующим здесь. Ничего на доске не изменится и не перезапишется."),
        ticks.node,
        el("div", { class: "u-row u-gap-sm u-wrap" }, all, run),
        status);
      all.addEventListener("click", () => ticks.toggleAll());
      run.addEventListener("click", () => {
        run.disabled = true;
        void append(bundle, bytesOf, ticks.picked(), status).finally(() => { run.disabled = false; });
      });
      shell.open({ icon: "file-up", title: `Импорт — ${file.name}`, body, onClose: () => {} });
    },
  };

  async function append(bundle: Bundle, bytesOf: AttachmentBytes, units: BundleUnit[], status: HTMLElement): Promise<void> {
    const log = (line: string): void => { status.textContent = line; };
    if (!units.length) {
      log("Отметьте хотя бы один список.");
      return;
    }
    // Re-encryption, uploads and the timeline import all go straight at the
    // API — the outbox cannot queue them, as with any cross-board write.
    if (!xySync.requireOnline("Импорт архива доступен только онлайн.", status)) return;
    const slice = sliceBundle(bundle, units.flatMap((u) => u.listIds));
    board.setStatus("saving");
    try {
      await checkQuota(slice);
      const append: AppendState = {
        labels: board.state.labels.map((l) => ({ id: l.id, name: l.name, color: l.color })),
        sessions: board.state.sessions.map((s) => ({ id: s.id, meta: s.meta })),
        lastRank: [...board.state.lists].map((l) => l.rank).sort().pop() ?? null,
        sourceName: bundle.board.name,
      };
      const result = await applyBundle(slice, { boardId: board.id, dk: board.dk(), append }, bytesOf, log);
      await board.reload();
      board.setStatus(result.failed ? "error" : "saved");
      if (!result.failed) {
        log(summarize(slice, result));
        return;
      }
      const failed = result.units.find((u) => u.error)!;
      const done = result.units.filter((u) => !u.error).map((u) => u.title);
      log(`«${failed.title}» не загрузился (${failed.error}) и откачен. `
        + (done.length ? `Загружено: ${done.join(", ")}. Откройте файл снова и отметьте остальное.` : "Доска не изменилась."));
    } catch (e) {
      board.setStatus("error");
      log("Не получилось: " + errMsg(e));
    }
  }
}
