// fontspanel.ts — the board's Font Families (ADR-0025), from the ☰ menu: what
// is uploaded, which Faces each has and how big it is, an upload, and a
// delete that is refused while a picture is set in the Family. Only the
// client can read the pictures, so the check is made here.
import { xyApp } from "./app.js";
import { iconed } from "./icons_gen.js";
import { humanSize } from "./attachments.js";
import { xySync } from "./sync.js";
import S from "./i18nstrings.js";
import type { BoardFonts } from "./boardfonts.js";
import type { Board, BoardPanel, PanelShell } from "./panels.js";
import type { BoardFont } from "./unlock.js";

const { el, errMsg, jdelete } = xyApp;

export interface FontsPanelDeps {
  fonts: BoardFonts;
  picturesUsing(hash: string): Promise<string[]>;
}

export function createFontsPanel(board: Board, shell: PanelShell, deps: FontsPanelDeps): BoardPanel {
  const { fonts } = deps;
  const list = el("div", { class: "u-col u-gap-sm" });
  const status = el("p", { class: "hint" });
  const file = el("input", { type: "file", accept: ".ttf,.otf,.zip", multiple: true, hidden: true }) as HTMLInputElement;

  // A Family that a newer one of its name replaced, still read by the
  // pictures not yet moved onto the new one.
  const replaced = (f: BoardFont): boolean => !fonts.families().some((x) => x.hash === f.hash);

  function row(f: BoardFont): HTMLElement {
    const remove = el("button", { class: "btn btn-ghost", type: "button", title: S.board.fonts.delete() }, ...iconed("trash-2", S.board.fonts.delete()));
    remove.addEventListener("click", () => { void del(f); });
    const what = [f.faces.map((x) => x.style).join(", "), humanSize(f.size)];
    if (replaced(f)) what.push(S.board.fonts.replaced());
    return el("div", { class: "u-row u-gap-sm u-align-center" },
      el("div", { class: "u-col u-grow" }, el("strong", { text: f.name }), el("span", { class: "hint", text: what.join(" · ") })),
      remove);
  }

  function render(): void {
    const rows = [...board.state.fonts].sort((a, b) => a.name.localeCompare(b.name) || b.id - a.id);
    list.replaceChildren(...(rows.length ? rows.map(row) : [el("p", { class: "hint", text: S.board.fonts.empty() })]));
  }

  async function del(f: BoardFont): Promise<void> {
    if (!xySync.requireOnline(S.board.fonts.offline(), status)) return;
    status.textContent = S.board.fonts.checking();
    try {
      const using = await deps.picturesUsing(f.hash);
      if (using.length) { status.textContent = S.board.fonts.inUse(f.name, using.join(", ")); return; }
      if (!confirm(S.board.fonts.confirmDelete(f.name))) { status.textContent = ""; return; }
      await jdelete(`/api/fonts/${f.id}`);
      fonts.forget(f);
      status.textContent = "";
      render();
    } catch (err) { status.textContent = errMsg(err); }
  }

  file.addEventListener("change", () => {
    const picked = [...(file.files ?? [])];
    file.value = "";
    if (!picked.length || !fonts.upload) return;
    status.textContent = S.xyimage.status.fontUploading();
    fonts.upload(picked).then(() => { status.textContent = ""; render(); }, (err: unknown) => { status.textContent = errMsg(err); });
  });

  return {
    id: "fonts", menu: "board", icon: "text-cursor-input",
    label: S.board.fonts.label(), title: S.board.fonts.title(),
    open() {
      render();
      status.textContent = "";
      const upload = el("button", { class: "btn", type: "button" }, ...iconed("file-up", S.board.fonts.upload()));
      upload.addEventListener("click", () => file.click());
      shell.open({
        icon: "text-cursor-input", title: S.board.fonts.title(),
        body: el("div", { class: "u-col u-gap-sm" }, el("p", { class: "hint", text: S.board.fonts.intro() }), list, el("div", { class: "u-row" }, upload), file, status),
      });
    },
  };
}
