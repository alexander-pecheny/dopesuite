// passcheck.ts — the Passphrase Check (CONTEXT.md). A device that has unlocked
// a board once keeps the key, not the words, so the board stays readable for
// years after its passphrase is forgotten — and the day that cache is lost the
// board is gone. Once a month, on a device holding the key, the owner is asked
// to type the words again.
//
// It has no "Later": the two ways out are typing the passphrase and setting a
// new one. Everything is injected (clock, storage, modal, the verify call), so
// the whole rule runs on plain objects in jstest.

import S from "./i18nstrings.js";
import type { PassphraseCopy } from "./passphrasetg.js";

export const PASSCHECK_PREFIX = "xy-passcheck:";
const PASSCHECK_INTERVAL_DAYS = 30;
const HOURS_PER_DAY = 24;
export const PASSCHECK_INTERVAL_MS = PASSCHECK_INTERVAL_DAYS * HOURS_PER_DAY * 60 * 60 * 1000;

type StorageLike = Pick<Storage, "getItem" | "setItem">;

// The clock is per board AND per device: what one phone has proved says nothing
// about the laptop, which is the whole point of asking.
export interface PassCheckStore {
  read(boardId: number): number | null;
  write(boardId: number, at: number): void;
}

export function passCheckStore(storage: StorageLike): PassCheckStore {
  return {
    read(boardId) {
      const at = Number(storage.getItem(PASSCHECK_PREFIX + boardId));
      return Number.isFinite(at) && at > 0 ? at : null;
    },
    write(boardId, at) {
      storage.setItem(PASSCHECK_PREFIX + boardId, String(at));
    },
  };
}

// stampPassCheck marks the passphrase as freshly known on this device — what
// the create and import pages call, so a board's first month is quiet.
export function stampPassCheck(boardId: number): void {
  try { passCheckStore(localStorage).write(boardId, Date.now()); } catch (_) {}
}

// What the board knows once its snapshot has rendered.
export interface PassCheckWhen {
  owner: boolean;
  // The key came out of the cache rather than off the unlock overlay — the
  // typed path has just proved the words, so there is nothing to ask.
  cached: boolean;
  online: boolean;
  testMode: boolean;
  // A ?card= deep link: the reader came for one question, not for a quiz.
  deepLink: boolean;
}

export interface PassCheckUI {
  modal: {
    open(opts?: { confirm?(): Promise<boolean> }): void;
    message(text: string): void;
  };
  // The modal's own <h2>: the second step answers the question the first asks,
  // and a heading still asking it reads as a contradiction.
  title: { textContent: string | null };
  step1: { hidden: boolean | string };
  step2: { hidden: boolean | string };
  form: { addEventListener(type: "submit", handler: (e: { preventDefault(): void }) => void): void };
  pass: { value: string; focus(): void };
  forgot: { addEventListener(type: "click", handler: () => void): void };
  // Step 2 says one of two things: the words are in the owner's Telegram, or
  // (no Telegram to send to) it is a good time to download a backup.
  sentHint: { hidden: boolean | string };
  backupHint: { hidden: boolean | string };
  backup: { hidden: boolean | string; addEventListener(type: "click", handler: () => void): void };
  // The close button: secondary next to the backup button, primary once it is alone.
  close: { classList: { toggle(name: string, on: boolean): void } };
}

export interface PassCheckDeps {
  ui: PassCheckUI;
  now(): number;
  read(): number | null;
  write(at: number): void;
  // Throws when the words are wrong; the thrown message is what the modal shows.
  verify(passphrase: string): Promise<void>;
  // Sends the typed words to the owner's Telegram unless a copy of the current
  // ones is already there (passphrasetg.ts).
  send(passphrase: string): Promise<PassphraseCopy>;
  // "Forgot the passphrase, change it" — the change form, which sends the new
  // words itself and calls back on success.
  changePass(onDone: (copy: PassphraseCopy) => void): void;
  // "Save backup" — the ☰ archive panel.
  backup(): void;
}

export interface PassCheck {
  // Runs once the board has rendered; asks at most once per page load.
  maybe(when: PassCheckWhen): void;
  // The words were proved some other way (a typed unlock, a change).
  stamp(): void;
}

export function createPassCheck(deps: PassCheckDeps): PassCheck {
  const { ui } = deps;
  let asked = false;

  const stamp = (): void => deps.write(deps.now());

  // The second step: the clock is stamped here, not at close, so a reader who
  // walks away after proving the words is not asked again tomorrow.
  function proved(copy: PassphraseCopy): void {
    stamp();
    ui.title.textContent = S.passphrase.check.title();
    ui.sentHint.hidden = !copy.kept;
    ui.backupHint.hidden = copy.kept;
    ui.backup.hidden = copy.kept;
    ui.close.classList.toggle("btn-primary", copy.kept);
    ui.close.classList.toggle("btn-ghost", !copy.kept);
    ui.step1.hidden = true;
    ui.step2.hidden = false;
  }

  ui.form.addEventListener("submit", async (e) => {
    e.preventDefault();
    ui.modal.message("");
    try {
      await deps.verify(ui.pass.value);
    } catch (err) {
      ui.modal.message(err instanceof Error ? err.message : String(err));
      return;
    }
    proved(await deps.send(ui.pass.value));
  });
  ui.forgot.addEventListener("click", () => deps.changePass(proved));
  ui.backup.addEventListener("click", () => deps.backup());

  return {
    stamp,
    maybe(when) {
      if (asked) return;
      if (!when.owner || !when.cached || !when.online || when.testMode || when.deepLink) return;
      const last = deps.read();
      if (last != null && deps.now() - last < PASSCHECK_INTERVAL_MS) return;
      asked = true;
      ui.pass.value = "";
      ui.step1.hidden = false;
      ui.step2.hidden = true;
      // The veto every dismissal gesture runs through: until the words are known
      // again there is nothing to close onto.
      ui.modal.open({ confirm: async () => ui.step2.hidden === false });
      ui.pass.focus();
    },
  };
}
