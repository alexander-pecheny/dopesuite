// changepass.ts — "Change the board's passphrase", the owner's way out of a
// forgotten passphrase while a device still holds the key. The passphrase only
// ever wraps the data key, so a change re-wraps the SAME key: nothing is
// re-encrypted, and the old words are never asked for — holding the key IS the
// proof, and someone who remembered the old ones would not be here.

import type { PassphraseSetup } from "./app.js";
import type { BoardKeymeta, DataKey } from "./crypto.js";
import type { BoardPanel } from "./panels.js";
import S from "./i18nstrings.js";

export interface ChangePassUI {
  modal: {
    open(): void;
    close(): void;
    message(text: string): void;
  };
  form: { addEventListener(type: "submit", handler: (e: { preventDefault(): void }) => void): void };
  input: { value: string };
  setup: PassphraseSetup;
}

export interface ChangePassDeps {
  boardId: number;
  ui: ChangePassUI;
  owner(): boolean;
  dk(): DataKey;
  crypto: {
    validatePassphrase(passphrase: string): string | null;
    rewrapKey(passphrase: string, dk: DataKey): Promise<Omit<BoardKeymeta, "verify_token">>;
  };
  // Reports into the modal's message and returns false when offline.
  requireOnline(message: string): boolean;
  jput(url: string, body: unknown): Promise<unknown>;
  errMsg(err: unknown): string;
  // The passphrase is freshly known again — stamp the Passphrase Check clock.
  onChanged(): void;
}

export interface ChangePass {
  panel: BoardPanel;
  // The same form the ☰ opens, with a callback for the Passphrase Check, which
  // treats a change as an answer.
  open(onDone?: () => void): void;
}

export function createChangePass(deps: ChangePassDeps): ChangePass {
  const { ui } = deps;
  let done: (() => void) | null = null;

  function open(onDone?: () => void): void {
    done = onDone ?? null;
    ui.setup.reset();
    void ui.setup.roll(true);
    ui.modal.open();
  }

  async function submit(): Promise<void> {
    const pass = ui.input.value;
    const bad = deps.crypto.validatePassphrase(pass);
    if (bad) { ui.modal.message(bad); return; }
    if (!deps.requireOnline(S.passphrase.change.offline())) return;
    try {
      const keymeta = await deps.crypto.rewrapKey(pass, deps.dk());
      await deps.jput(`/api/boards/${deps.boardId}/keymeta`, keymeta);
    } catch (err) {
      ui.modal.message(deps.errMsg(err));
      return;
    }
    deps.onChanged();
    ui.modal.close();
    const after = done;
    done = null;
    after?.();
  }

  ui.form.addEventListener("submit", async (e) => { e.preventDefault(); await submit(); });

  return {
    open,
    panel: {
      id: "change-pass", menu: "board", icon: "lock",
      label: S.passphrase.change.label(),
      title: S.passphrase.change.title(),
      offered: () => deps.owner(),
      open: () => open(),
    },
  };
}
