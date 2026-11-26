// The parked panel: one floating panel on <body> that a control on a sheet
// opens under itself (the seat picker's tickboxes, KSI's sticker menu). A
// sheet draws dozens of such controls and none of them keeps its own copy: the
// panel is made when one opens and goes when it closes, a click outside or Esc
// closes it, and it follows its control as the sheet scrolls in either
// direction, flipped above it near the bottom of the viewport. A sheet
// redrawn under the panel takes its control away, and the panel goes with it.

import {clamp, floatingPopover} from "./widgets.js";

// Space the panel keeps from the viewport edge, and its smallest height.
const PANEL_MARGIN_PX = 8;
const PANEL_MIN_HEIGHT_PX = 120;
// The gap between the control and the panel.
const PANEL_GAP_PX = 2;

let panelNode: HTMLElement | null = null;
let panelTrigger: HTMLElement | null = null;

// isParkedOn says whether the panel open now belongs to trigger.
export function isParkedOn(trigger: HTMLElement): boolean {
  return panelTrigger === trigger;
}

// parkPanel shows panel under trigger, closing whatever panel was open.
export function parkPanel(trigger: HTMLElement, panel: HTMLElement): void {
  closeParkedPanel();
  document.body.appendChild(panel);
  panelNode = panel;
  panelTrigger = trigger;
  trigger.setAttribute("aria-expanded", "true");
  // The hover popovers stand down while a panel is open: two floating surfaces
  // over one cell is one too many.
  document.documentElement.classList.add("seat-picker-open");
  floatingPopover().hide();
  positionPanel();
  document.addEventListener("pointerdown", onPointerDown, true);
  document.addEventListener("keydown", onKeydown, true);
  window.addEventListener("scroll", positionPanel, {capture: true, passive: true});
  window.addEventListener("resize", positionPanel);
}

export function closeParkedPanel(): void {
  if (!panelNode) return;
  panelNode.remove();
  panelNode = null;
  panelTrigger?.setAttribute("aria-expanded", "false");
  panelTrigger = null;
  document.documentElement.classList.remove("seat-picker-open");
  document.removeEventListener("pointerdown", onPointerDown, true);
  document.removeEventListener("keydown", onKeydown, true);
  window.removeEventListener("scroll", positionPanel, {capture: true} as EventListenerOptions);
  window.removeEventListener("resize", positionPanel);
}

function onPointerDown(event: PointerEvent): void {
  if (!(event.target instanceof Node)) return;
  if (panelNode?.contains(event.target) || panelTrigger?.contains(event.target)) return;
  closeParkedPanel();
}

// Esc closes the panel and hands the focus back to its control, so the next
// arrow key moves the sheet cursor rather than the panel's rows.
function onKeydown(event: KeyboardEvent): void {
  if (event.key !== "Escape" || !panelNode) return;
  const trigger = panelTrigger;
  event.preventDefault();
  event.stopPropagation();
  closeParkedPanel();
  trigger?.focus();
}

function positionPanel(): void {
  const panel = panelNode;
  const trigger = panelTrigger;
  if (!panel || !trigger) return;
  if (!trigger.isConnected) {
    closeParkedPanel();
    return;
  }
  const rect = trigger.getBoundingClientRect();
  const margin = PANEL_MARGIN_PX;
  panel.style.position = "fixed";
  panel.style.maxHeight = `${Math.max(PANEL_MIN_HEIGHT_PX, window.innerHeight - 2 * margin)}px`;
  const width = panel.offsetWidth;
  const height = panel.offsetHeight;
  const left = clamp(rect.left, margin, Math.max(margin, window.innerWidth - width - margin));
  const below = rect.bottom + margin + height <= window.innerHeight;
  const top = below ? rect.bottom + PANEL_GAP_PX : Math.max(margin, rect.top - height - PANEL_GAP_PX);
  panel.style.left = `${left}px`;
  panel.style.top = `${top}px`;
}
