// copyboard.ts — "Clone Board": a new board holding a full replica of this one
// (the string ids say copy; only the wording is clone).
// The source is already decrypted here (the board page), so it becomes a Bundle
// (buildBundle) through the same path an archive export or a cross-board list
// move takes, and createBoardFromBundle re-encrypts it under a fresh key into a
// brand-new board. Nothing new has to reach the server — the source's content
// and the target's key both live in this browser.

import { xyApp } from "./app.js";
import type { PassphraseSetup } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { buildBundle } from "./bundleexport.js";
import { createBoardFromBundle } from "./bundleimport.js";
import type { Board, BoardPanel, PanelShell } from "./panels.js";
import { icon } from "./icons_gen.js";
import { xySync } from "./sync.js";
import S from "./i18nstrings.js";

const { el, errMsg } = xyApp;

export function createCopyBoardPanel(board: Board, shell: PanelShell): BoardPanel {
  return {
    id: "copy-board", menu: "board", icon: "copy",
    label: S.board.copy.label(),
    title: S.board.copy.title(),
    open() {
      const nameId = "copyName";
      const nameInput = el("input", {
        id: nameId, class: "input", type: "text",
        value: board.state.name + S.board.copy.nameSuffix(),
        autocomplete: "off", spellcheck: "false",
      }) as HTMLInputElement;
      const passInput = el("input", {
        class: "input", type: "text",
        autocomplete: "new-password", spellcheck: "false",
      }) as HTMLInputElement;
      const dice = el("button", {
        type: "button", class: "btn btn-ghost btn-small", title: S.board.copy.genPass(), "aria-label": S.board.copy.genPass(),
      }, icon("dices")) as HTMLButtonElement;
      const copied = el("p", { class: "muted", hidden: "hidden" }, S.board.copy.passCopied());
      const saved = el("label", {},
        el("input", { type: "checkbox" }) as HTMLInputElement,
        el("span", {}, ` ${S.board.copy.passSaved()}`),
      );
      const check = saved.querySelector("input")! as HTMLInputElement;
      const submit = el("button", { type: "submit", class: "btn btn-primary", disabled: "disabled" }, S.board.copy.submit()) as HTMLButtonElement;
      const status = el("p", { class: "hint" });

      const setup: PassphraseSetup = xyApp.wirePassphraseSetup({
        input: passInput, dice, copied,
        saved: check, submit,
      }, xyCrypto.generatePassphrase);
      // The passphrase is rolled and copied inside the menu-row click, not on
      // submit: the clipboard only answers to a user gesture, and this is the one
      // moment the words are ever shown in the clear (same ritual as create).
      setup.reset();
      void setup.roll(true);

      const form = el("form", { class: "u-col u-gap-sm" },
        el("label", { class: "fld-label", for: nameId, text: S.board.copy.nameLabel() }),
        nameInput,
        el("div", { class: "u-row u-gap-sm u-align-center" }, passInput, dice),
        copied,
        el("p", { class: "hint", text: S.board.copy.passHint() }),
        el("p", { class: "hint hint-danger", text: S.board.copy.passDanger() }),
        saved,
        el("div", { class: "u-row u-gap-sm u-wrap" }, submit),
        status,
      );

      form.addEventListener("submit", async (e) => {
        e.preventDefault();
        const name = nameInput.value.trim();
        const pass = passInput.value;
        if (!name) { status.textContent = S.board.copy.nameRequired(); return; }
        const passErr = xyCrypto.validatePassphrase(pass);
        if (passErr) { status.textContent = passErr; return; }
        if (!xySync.requireOnline(S.board.copy.offline(), status)) return;
        submit.disabled = true;
        const log = (line: string): void => { status.textContent = line; };
        try {
          // buildBundle with listIds null means every list — the whole board.
          // The Bundle is plaintext in memory; createBoardFromBundle re-encrypts
          // it under the new board's DK, and deletes the board if the copy dies
          // part-way (bundles are the unit of atomicity, not the copy).
          const { bundle, bytesOf } = await buildBundle(board, null, log);
          const { id } = await createBoardFromBundle(bundle, bytesOf, name, pass, log);
          log(S.board.copy.done());
          window.location.href = `/board/${id}`;
        } catch (err) {
          status.textContent = S.board.copy.failed(errMsg(err));
          // Stay truthful to the promise gate: enable only when still promised.
          submit.disabled = !check.checked;
        }
      });

      shell.open({ icon: "copy", title: S.board.copy.label(), body: form, onClose: () => setup.reset() });
    },
  };
}
