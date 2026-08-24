// bundleimportpanel.ts — «Загрузить (.zip)…»: a Bundle appended to the board
// that is open. The mirror of the export panel, and the reason it lives on the
// board page rather than on /import — appending re-encrypts under this board's
// key, which only its own page holds.
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
import { checkQuota, readBundleFile, summarize } from "./bundleimport.js";
import { sliceBundle } from "./bundle.js";
import { tickList } from "./bundleexport.js";
import type { Board, BoardPanel, PanelShell } from "./panels.js";

const { el, errMsg } = xyApp;

export function createBundleImportPanel(board: Board, shell: PanelShell): BoardPanel {
  return {
    id: "bundle-import", menu: "board", icon: "file-up",
    label: "Загрузить (.zip)…",
    title: "Добавить списки из архива xy на эту доску",
    open() {
      const status = el("p", { class: "hint" }, "");
      const picked = el("div", { class: "u-col u-gap-sm" });
      const file = el("input", { type: "file", accept: ".zip,application/zip", class: "input" }) as HTMLInputElement;
      const body = el("div", { class: "u-col u-gap-sm" },
        el("p", { class: "hint" },
          "Архив xy (☰ «Скачать (.zip)…» на любой доске). Отмеченные списки добавятся ",
          "к этой доске в конец; метки и тесты подхватятся к уже существующим здесь. ",
          "Ничего на доске при этом не изменится и не перезапишется."),
        file, picked, status);

      file.addEventListener("change", () => {
        picked.replaceChildren();
        status.textContent = "";
        const f = file.files?.[0];
        if (f) void openArchive(f);
      });

      async function openArchive(f: File): Promise<void> {
        let bundle: Bundle, bytesOf: AttachmentBytes;
        try {
          status.textContent = "Читаю архив…";
          ({ bundle, bytesOf } = await readBundleFile(f));
        } catch (e) {
          status.textContent = "Не получилось: " + errMsg(e);
          return;
        }
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
        picked.replaceChildren(
          el("p", { class: "hint" },
            `Доска «${bundle.board.name}», выгружена ${bundle.exported_at.slice(0, 10)}.`),
          ticks.node,
          el("div", { class: "u-row u-gap-sm u-wrap" }, all, run),
        );
        all.addEventListener("click", () => ticks.toggleAll());
        run.addEventListener("click", () => {
          run.disabled = true;
          void append(bundle, bytesOf, ticks.picked()).finally(() => { run.disabled = false; });
        });
        status.textContent = "";
      }

      async function append(bundle: Bundle, bytesOf: AttachmentBytes, units: BundleUnit[]): Promise<void> {
        const log = (line: string): void => { status.textContent = line; };
        if (!units.length) {
          log("Отметьте хотя бы один список.");
          return;
        }
        // Re-encryption, uploads and the timeline import all go straight at the
        // API — the outbox cannot queue them, as with any cross-board write.
        if (!xySync.requireOnline("Загрузка архива доступна только онлайн.", status)) return;
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

      shell.open({ icon: "file-up", title: "Загрузить (.zip)", body, onClose: () => {} });
    },
  };
}
