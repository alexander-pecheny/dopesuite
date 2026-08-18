// modal.ts — one lifecycle for every plain modal: a `modal` block from
// board.dopeui (or profile/index) whose ids follow the <stem>Overlay /
// <stem>Close / <stem>Cancel / <stem>Message convention. The handle owns
// `hidden`, the overlay-stack registration (so back, Escape, ✕ and the backdrop
// are one gesture), the close buttons and the message node; a caller fills the
// body, calls open(), and hands over whatever teardown its state needs.

import { type OverlayStack, overlayStack } from "./overlaystack.js";
import { xyApp } from "./app.js";

export interface Modal {
  readonly el: HTMLElement;
  // Show and register. `onClose` runs on every dismissal (any gesture);
  // `confirm` may veto one, as the stack's OverlayEntry does.
  open(opts?: { onClose?(): void | Promise<void>; confirm?(): Promise<boolean> }): void;
  // Dismiss through the stack, if this modal is the topmost overlay.
  close(): void;
  message(text: string): void;
  readonly isOpen: boolean;
}

interface ModalDeps {
  byId<T extends HTMLElement = HTMLElement>(id: string): T;
  stack: Pick<OverlayStack, "open" | "pop" | "isTop">;
}

export function createModal(stem: string, deps: ModalDeps): Modal {
  const el = deps.byId(stem + "Overlay");
  const optional = (id: string): HTMLElement | null => { try { return deps.byId(id); } catch (_) { return null; } };
  const close = (): void => { if (deps.stack.isTop(el)) deps.stack.pop(); };
  for (const id of [stem + "Close", stem + "Cancel"]) optional(id)?.addEventListener("click", close);
  el.addEventListener("pointerdown", (e) => { if (e.target === el) close(); });
  let messageNode: HTMLElement | null | undefined;
  function message(text: string): void {
    if (messageNode === undefined) messageNode = optional(stem + "Message");
    if (messageNode) messageNode.textContent = text;
    else if (text !== "") throw new Error(`page is missing #${stem}Message`);
  }
  function open(opts: { onClose?(): void | Promise<void>; confirm?(): Promise<boolean> } = {}): void {
    message("");
    if (!el.hidden) return;
    el.hidden = false;
    deps.stack.open({
      el,
      close: async () => { el.hidden = true; await opts.onClose?.(); },
      confirm: opts.confirm,
    });
  }
  return { el, open, close, message, get isOpen() { return !el.hidden; } };
}

// modal(stem) is the page's spelling: the document's ids and the page's stack.
export function modal(stem: string): Modal {
  return createModal(stem, { byId: xyApp.byId, stack: overlayStack });
}
