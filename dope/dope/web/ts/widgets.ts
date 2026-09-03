// Interaction widgets shared by the game pages: cell nav bar, virtual keypad,
// floating popovers, sync-status dot, team-name overflow, cell range selection,
// and the viewer counter. DOM-only — no table building, no sync.

import S from "./i18nstrings.js";

export function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value));
}

export interface CellNavBarOptions {
  onPrev?: () => void;
  onNext?: () => void;
  prevLabel?: string;
  nextLabel?: string;
}

export interface CellNavBar {
  show(): void;
  hide(): void;
}

// installCellNavBar mounts a floating ↑/↓ bar pinned just above the on-screen
// keyboard for advancing between editable cells. Mobile numeric keypads
// (inputmode=numeric/decimal) have no Return key on iOS, so this is the only
// way to step cell-to-cell without dismissing the keypad. Rendered only on
// coarse-pointer (touch) devices — on desktop, Enter/Tab already do this.
//
// The caller drives visibility with show()/hide(); buttons fire onPrev/onNext
// on `pointerdown` with the default prevented, so the focused input is never
// blurred and the keyboard stays up while we programmatically move focus.
export function installCellNavBar(options: CellNavBarOptions = {}): CellNavBar {
  const coarse = typeof window.matchMedia === "function" &&
    window.matchMedia("(pointer: coarse)").matches;
  if (!coarse) return {show: () => {}, hide: () => {}};

  const {onPrev, onNext, prevLabel = "▲", nextLabel = "▼"} = options;
  const bar = document.createElement("div");
  bar.className = "entry-nav-bar";
  bar.hidden = true;
  const make = (label: string, aria: string, handler?: () => void) => {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = label;
    button.setAttribute("aria-label", aria);
    button.addEventListener("pointerdown", (event) => {
      event.preventDefault();
      handler?.();
    });
    return button;
  };
  bar.append(
    make(prevLabel, S.widgets.cellNav.prev(), onPrev),
    make(nextLabel, S.widgets.cellNav.next(), onNext),
  );
  document.body.appendChild(bar);

  let visible = false;
  // Pin to the visual viewport's box (see installVirtualKeypad): iOS resolves
  // fixed + right:0 against the document width when the page scrolls
  // horizontally, overflowing the screen and skewing the arrows.
  const position = () => {
    if (!visible) return;
    const vv = window.visualViewport;
    if (vv) {
      bar.style.left = `${Math.round(vv.offsetLeft)}px`;
      bar.style.right = "auto";
      bar.style.width = `${Math.round(vv.width)}px`;
      bar.style.top = `${Math.round(vv.offsetTop + vv.height - bar.offsetHeight)}px`;
      bar.style.bottom = "auto";
    } else {
      bar.style.left = "0px";
      bar.style.right = "0px";
      bar.style.width = "auto";
      bar.style.top = "auto";
      bar.style.bottom = "0px";
    }
  };
  const vv = window.visualViewport;
  if (vv) {
    vv.addEventListener("resize", position);
    vv.addEventListener("scroll", position);
  }
  return {
    show() {
      visible = true;
      bar.hidden = false; // unhide before measuring offsetHeight
      position();
    },
    hide() {
      visible = false;
      bar.hidden = true;
    },
  };
}

export interface VirtualKeypadOptions {
  onDigit?: (digit: string) => void;
  onBackspace?: () => void;
  onNav?: (dx: number, dy: number) => void;
}

export interface VirtualKeypad {
  show(): void;
  hide(): void;
  visible(): boolean;
  height(): number;
}

// installVirtualKeypad mounts a full on-screen numeric keypad pinned to the
// bottom of the visual viewport. It replaces the OS keyboard for digit-only
// cell entry on touch devices: the host <input> sets inputmode="none" so
// iOS/Android suppress their native keypad (which looks out of place and,
// on iOS, lacks a Return key), and these keys drive the input via callbacks.
// Layout: a navigation row (← ↑ ↓ →) above a 3-column digit pad (1–9, then a
// double-width 0 and ⌫). Rendered only on coarse-pointer devices — on desktop
// the physical keyboard and arrow-key navigation already cover this, so it
// returns no-ops. Buttons fire on `pointerdown` with the default prevented so
// the focused input is never blurred and its caret/selection survive editing.
export function installVirtualKeypad(options: VirtualKeypadOptions = {}): VirtualKeypad {
  const coarse = typeof window.matchMedia === "function" &&
    window.matchMedia("(pointer: coarse)").matches;
  if (!coarse) return {show: () => {}, hide: () => {}, visible: () => false, height: () => 0};

  const {onDigit, onBackspace, onNav} = options;
  const pad = document.createElement("div");
  pad.className = "entry-keypad";
  pad.hidden = true;

  const key = (label: string, aria: string, className: string, handler?: () => void) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = className;
    button.textContent = label;
    button.setAttribute("aria-label", aria);
    button.addEventListener("pointerdown", (event) => {
      event.preventDefault();
      handler?.();
    });
    return button;
  };

  const navRow = document.createElement("div");
  navRow.className = "entry-keypad-nav";
  navRow.append(
    key("←", S.widgets.keypad.prevColumn(), "entry-keypad-key entry-keypad-arrow", () => onNav?.(-1, 0)),
    key("↑", S.widgets.keypad.prevRow(), "entry-keypad-key entry-keypad-arrow", () => onNav?.(0, -1)),
    key("↓", S.widgets.keypad.nextRow(), "entry-keypad-key entry-keypad-arrow", () => onNav?.(0, 1)),
    key("→", S.widgets.keypad.nextColumn(), "entry-keypad-key entry-keypad-arrow", () => onNav?.(1, 0)),
  );

  const digits = document.createElement("div");
  digits.className = "entry-keypad-digits";
  for (let n = 1; n <= 9; n++) {
    digits.appendChild(key(String(n), String(n), "entry-keypad-key", () => onDigit?.(String(n))));
  }
  digits.appendChild(key("0", "0", "entry-keypad-key entry-keypad-zero", () => onDigit?.("0")));
  digits.appendChild(key("⌫", S.widgets.keypad.backspace(), "entry-keypad-key entry-keypad-back", () => onBackspace?.()));

  pad.append(navRow, digits);
  document.body.appendChild(pad);

  let isVisible = false;
  // Pin to the visual viewport's box explicitly. iOS Safari resolves
  // position:fixed + right:0 against the document width when the page scrolls
  // horizontally (our entry table is wide), which overflows the screen — so we
  // set left/width/top from visualViewport instead of relying on left/right:0.
  const position = () => {
    if (!isVisible) return;
    const vv = window.visualViewport;
    if (vv) {
      pad.style.left = `${Math.round(vv.offsetLeft)}px`;
      pad.style.right = "auto";
      pad.style.width = `${Math.round(vv.width)}px`;
      pad.style.top = `${Math.round(vv.offsetTop + vv.height - pad.offsetHeight)}px`;
      pad.style.bottom = "auto";
    } else {
      pad.style.left = "0px";
      pad.style.right = "0px";
      pad.style.width = "auto";
      pad.style.top = "auto";
      pad.style.bottom = "0px";
    }
  };
  const vv = window.visualViewport;
  if (vv) {
    vv.addEventListener("resize", position);
    vv.addEventListener("scroll", position);
  }
  return {
    show() {
      isVisible = true;
      pad.hidden = false; // unhide before measuring offsetHeight
      position();
    },
    hide() {
      isVisible = false;
      pad.hidden = true;
    },
    visible: () => isVisible,
    height: () => (isVisible ? pad.offsetHeight : 0),
  };
}

export interface FloatingPopoverSpec {
  trigger: string;
  popover: string;
  anchor: string;
}

export interface FloatingPopoverOptions {
  root?: Element | Document | null;
  specs?: FloatingPopoverSpec[];
}

export interface FloatingPopover {
  bind(): void;
  hide(): void;
  position(): void;
}

export function createFloatingPopover(options: FloatingPopoverOptions): FloatingPopover {
  const root = options.root;
  const specs = options.specs || [];
  if (!root || specs.length === 0) {
    return {bind: () => {}, hide: () => {}, position: () => {}};
  }

  let popoverNode: HTMLElement | null = null;
  let active: {trigger: Element; spec: FloatingPopoverSpec} | null = null;

  function triggerFor(target: EventTarget | null): Element | null {
    if (!(target instanceof Element)) return null;
    for (const spec of specs) {
      const trigger = target.closest(spec.trigger);
      if (trigger && root!.contains(trigger)) return trigger;
    }
    return null;
  }

  function specFor(trigger: Element): FloatingPopoverSpec | null {
    return specs.find((spec) => trigger.matches(spec.trigger)) || null;
  }

  function ensureNode(): HTMLElement {
    if (!popoverNode) {
      popoverNode = document.createElement("div");
      popoverNode.className = "popover floating-name-popover";
      document.body.appendChild(popoverNode);
    }
    return popoverNode;
  }

  function show(trigger: Element): void {
    const spec = specFor(trigger);
    const source = spec ? trigger.querySelector(spec.popover) : null;
    const text = source?.textContent?.trim() || "";
    if (!spec || !text) {
      hide();
      return;
    }
    const popover = ensureNode();
    popover.textContent = text;
    popover.classList.add("visible");
    active = {trigger, spec};
    position();
  }

  function hide(): void {
    if (!popoverNode) return;
    popoverNode.classList.remove("visible", "above");
    popoverNode.textContent = "";
    popoverNode.style.removeProperty("top");
    popoverNode.style.removeProperty("left");
    popoverNode.style.removeProperty("max-width");
    active = null;
  }

  function position(): void {
    if (!active || !popoverNode) return;
    const {trigger, spec} = active;
    if (!document.body.contains(trigger) || !trigger.matches(spec.trigger)) {
      hide();
      return;
    }
    const anchor = trigger.querySelector(spec.anchor) || trigger;
    const rect = anchor.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0 || rect.bottom < 0 || rect.top > window.innerHeight) {
      hide();
      return;
    }

    const margin = 8;
    const popover = popoverNode;
    popover.style.maxWidth = `${Math.max(80, Math.min(420, window.innerWidth - margin * 2))}px`;
    popover.style.visibility = "hidden";
    popover.classList.add("visible");

    const width = popover.offsetWidth;
    const height = popover.offsetHeight;
    const maxLeft = Math.max(margin, window.innerWidth - width - margin);
    const left = clamp(rect.left, margin, maxLeft);
    const belowTop = rect.bottom - 2;
    const aboveTop = rect.top - height + 2;
    const shouldOpenUp = belowTop + height > window.innerHeight - margin && rect.top > window.innerHeight - rect.bottom;
    const maxTop = Math.max(margin, window.innerHeight - height - margin);
    const top = clamp(shouldOpenUp ? aboveTop : belowTop, margin, maxTop);

    popover.classList.toggle("above", shouldOpenUp);
    popover.style.left = `${Math.round(left)}px`;
    popover.style.top = `${Math.round(top)}px`;
    popover.style.visibility = "";
  }

  function onPointerOver(event: PointerEvent): void {
    // On touch, pointerover fires while swiping across cells; showing here
    // would pop the popover on every swipe. Touch shows via tap (see onTapEnd).
    if (event.pointerType === "touch") return;
    const trigger = triggerFor(event.target);
    if (!trigger || active?.trigger === trigger) return;
    show(trigger);
  }

  let tapStart: {x: number; y: number} | null = null;
  const TAP_MOVE_THRESHOLD = 10;

  function onTapStart(event: PointerEvent): void {
    if (event.pointerType !== "touch") return;
    tapStart = {x: event.clientX, y: event.clientY};
  }

  function onTapEnd(event: PointerEvent): void {
    if (event.pointerType !== "touch" || !tapStart) return;
    const moved = Math.hypot(event.clientX - tapStart.x, event.clientY - tapStart.y);
    tapStart = null;
    if (moved > TAP_MOVE_THRESHOLD) return; // a swipe, not a tap
    const trigger = triggerFor(event.target);
    if (trigger) {
      if (active?.trigger !== trigger) show(trigger);
    } else {
      hide();
    }
  }

  function onPointerOut(event: PointerEvent): void {
    if (event.pointerType === "touch") return;
    const trigger = active?.trigger;
    if (!trigger || !(event.target instanceof Node) || !trigger.contains(event.target)) return;
    if (event.relatedTarget instanceof Node && trigger.contains(event.relatedTarget)) return;
    if (!trigger.matches(":focus-within")) hide();
  }

  function onFocusIn(event: FocusEvent): void {
    const trigger = triggerFor(event.target);
    if (trigger) show(trigger);
  }

  function onFocusOut(event: FocusEvent): void {
    const trigger = active?.trigger;
    if (!trigger || !(event.target instanceof Node) || !trigger.contains(event.target)) return;
    window.setTimeout(() => {
      if (!trigger.matches(":focus-within") && !trigger.matches(":hover")) hide();
    }, 0);
  }

  let positionFrame = 0;
  function schedulePosition(): void {
    if (positionFrame) return;
    positionFrame = requestAnimationFrame(() => {
      positionFrame = 0;
      position();
    });
  }

  function onPointerDownOutside(event: PointerEvent): void {
    if (!active || event.pointerType !== "touch") return;
    if (event.target instanceof Node && active.trigger.contains(event.target)) return;
    hide();
  }

  function bind(): void {
    document.documentElement.classList.add("floating-popovers-enabled");
    document.addEventListener("pointerover", onPointerOver);
    document.addEventListener("pointerout", onPointerOut);
    document.addEventListener("focusin", onFocusIn);
    document.addEventListener("focusout", onFocusOut);
    document.addEventListener("pointerdown", onPointerDownOutside, true);
    document.addEventListener("pointerdown", onTapStart, true);
    document.addEventListener("pointerup", onTapEnd, true);
    window.addEventListener("scroll", schedulePosition, {capture: true, passive: true});
  }

  return {bind, hide, position};
}

const SYNC_STATUS_LABELS: Record<string, string> = {
  saved: S.widgets.status.saved(),
  saving: S.widgets.status.saving(),
  reconnecting: S.widgets.status.reconnecting(),
  error: S.widgets.status.error(),
};

export function createStatusReporter(statusNode: HTMLElement | null | undefined): (state: string) => void {
  const node = statusNode;
  if (!node) return () => {};
  return function setStatus(state: string) {
    node.dataset.state = state;
    const label = SYNC_STATUS_LABELS[state] || SYNC_STATUS_LABELS.saving;
    node.setAttribute("aria-label", label);
    node.title = label;
  };
}

// The standalone ✏️/👀 icons were folded into the ☰ menu (menu.js).
// These now register the menu's context-aware jump item instead of mounting
// an icon; .refresh() re-points it after SPA navigation. statusNode is kept
// for call-site compatibility but unused.

export interface NameOverflowConfig {
  cellSelector: string;
  nameSelector: string;
  truncatedClass: string;
  citySelector?: string;
  cityTruncatedClass?: string;
}

export interface ScrollEdges {
  left: boolean;
  right: boolean;
}

export interface ScrollEdgeBinding {
  refresh(): void;
  dispose(): void;
}

// bindScrollEdges keeps a scroller's edge classes in sync with where it is
// scrolled to: `update` runs once now and on every scroll, coalesced to a frame.
// Seven pages used to hand-roll this, each repeating the same epsilon and two of
// them binding twice to the same element with no way to unbind.
export function bindScrollEdges(
  el: Element | null | undefined,
  update: (edges: ScrollEdges, el: Element) => void,
): ScrollEdgeBinding {
  if (!el) return {refresh() {}, dispose() {}};
  const target = el;
  let frame = 0;
  const refresh = (): void => {
    frame = 0;
    update({
      left: target.scrollLeft > 1,
      right: target.scrollLeft + target.clientWidth < target.scrollWidth - 1,
    }, target);
  };
  const onScroll = (): void => {
    if (!frame) frame = requestAnimationFrame(refresh);
  };
  refresh();
  target.addEventListener("scroll", onScroll, {passive: true});
  return {
    refresh,
    dispose() {
      if (frame) cancelAnimationFrame(frame);
      frame = 0;
      target.removeEventListener("scroll", onScroll);
    },
  };
}

// isClipped is the one definition of "this text does not fit its box", epsilon
// included. Every truncation cue in the app — the fade, the popover, the EK
// stage font-shrink — asks this question, so it gets asked in one place.
export function isClipped(el: Element | null | undefined): boolean {
  return Boolean(el && el.scrollWidth > el.clientWidth + 1);
}

// markNameOverflow flags every cell under `root` whose inner name (and optional
// city) is clipped, so the page can show a fade + popover. Reads are batched
// ahead of writes so the measure loop never triggers a reflow mid-pass.
export function markNameOverflow(root: ParentNode | null | undefined, cfg: NameOverflowConfig): void {
  if (!root) return;
  const cells = root.querySelectorAll(cfg.cellSelector);
  const readings = new Array<boolean>(cells.length);
  for (let i = 0; i < cells.length; i++) {
    readings[i] = isClipped(cells[i].querySelector(cfg.nameSelector));
  }
  for (let i = 0; i < cells.length; i++) {
    cells[i].classList.toggle(cfg.truncatedClass, readings[i]);
    if (cfg.citySelector && cfg.cityTruncatedClass) {
      const city = cells[i].querySelector(cfg.citySelector);
      city?.classList.toggle(cfg.cityTruncatedClass, isClipped(city));
    }
  }
}

export interface TeamNameOverflowController {
  schedule(targetRoot?: ParentNode): void;
  updateDetailed(targetRoot?: ParentNode): void;
  updateResults(targetRoot?: ParentNode): void;
}

export function createTeamNameOverflowController({root, detailed, results}: {
  root: ParentNode;
  detailed: NameOverflowConfig;
  results: NameOverflowConfig;
}): TeamNameOverflowController {
  function updateDetailed(targetRoot: ParentNode = root): void {
    markNameOverflow(targetRoot, detailed);
  }
  function updateResults(targetRoot: ParentNode = root): void {
    markNameOverflow(targetRoot, results);
  }
  let frame = 0;
  function schedule(targetRoot: ParentNode = root): void {
    if (frame) cancelAnimationFrame(frame);
    frame = requestAnimationFrame(() => {
      frame = 0;
      updateDetailed(targetRoot);
      updateResults(targetRoot);
    });
  }
  return {schedule, updateDetailed, updateResults};
}

// createViewerCounter renders a live "NN👀" concurrent-viewer tally
// immediately to the left of the sync-status tick. The span is created and
// inserted dynamically (no markup change needed) and stays hidden until a
// positive count arrives. setCount is driven by "viewers" SSE events.
export function createViewerCounter(statusNode: HTMLElement | null | undefined): {setCount(count: unknown): void} {
  if (!statusNode || !statusNode.parentElement) {
    return {setCount: () => {}};
  }
  const node = document.createElement("span");
  node.className = "viewers-count";
  node.hidden = true;
  node.setAttribute("aria-label", S.widgets.viewers.label());
  // Number and eyes are separate children so the flex `gap` spaces them — a
  // single "N👀" text node would render them touching.
  const num = document.createElement("span");
  const eyes = document.createElement("span");
  eyes.textContent = "\u{1F440}";
  eyes.setAttribute("aria-hidden", "true");
  node.append(num, eyes);
  statusNode.parentElement.insertBefore(node, statusNode);
  return {
    setCount(count) {
      const n = Number(count);
      if (!Number.isFinite(n) || n <= 0) {
        node.hidden = true;
        num.textContent = "";
        return;
      }
      num.textContent = String(n);
      node.title = S.widgets.viewers.title(String(n));
      node.hidden = false;
    },
  };
}

// renderTabBar fills a gametopbar tabs mount with .match-tab buttons — the one
// tab strip every game page shares (od/si/brain). The caller owns which tabs
// are visible and what selecting one does.
const tabBarScrollBindings = new WeakMap<HTMLElement, ScrollEdgeBinding>();
const tabBarActiveKeys = new WeakMap<HTMLElement, string>();

export function renderTabBar(
  root: HTMLElement,
  tabs: Array<{key: string; label: string}>,
  activeKey: string,
  onSelect: (key: string) => void,
): void {
  root.replaceChildren();
  for (const tab of tabs) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "match-tab" + (activeKey === tab.key ? " active" : "");
    btn.textContent = tab.label;
    btn.setAttribute("role", "tab");
    btn.setAttribute("aria-selected", activeKey === tab.key ? "true" : "false");
    btn.addEventListener("click", () => {
      if (tab.key !== activeKey) onSelect(tab.key);
    });
    root.appendChild(btn);
  }
  // A dozen tabs overflow at any width — fade whichever edge hides more, the
  // same treatment the EK tab bar gets.
  let binding = tabBarScrollBindings.get(root);
  if (!binding) {
    binding = bindScrollEdges(root, ({left, right}, bar) => {
      bar.classList.toggle("tabs-scroll-left", left);
      bar.classList.toggle("tabs-scroll-right", right);
    });
    tabBarScrollBindings.set(root, binding);
  } else {
    binding.refresh();
  }
  // Reveal the active tab only when it changed: a live-update rerender must
  // not yank the bar away from wherever the user scrolled it.
  if (tabBarActiveKeys.get(root) !== activeKey) {
    tabBarActiveKeys.set(root, activeKey);
    root.querySelector<HTMLElement>(".match-tab.active")?.scrollIntoView({block: "nearest", inline: "nearest"});
  }
}

// fitScrollFade caps a scroll frame's edge shadows (the kit's scroll-fade
// backgrounds) to its content box — viewport-fixed, they would otherwise run
// the frame's full height/width even where the table has long ended.
export function fitScrollFade(frame: Element | null | undefined): void {
  if (!frame || typeof ResizeObserver !== "function") return;
  const el = frame as HTMLElement;
  const apply = (): void => {
    const bounds = el.getBoundingClientRect();
    let width = 0;
    let height = 0;
    for (const child of el.children) {
      const rect = child.getBoundingClientRect();
      width = Math.max(width, rect.right - bounds.left + el.scrollLeft);
      height = Math.max(height, rect.bottom - bounds.top + el.scrollTop);
    }
    if (width > 0 && height > 0) {
      el.style.setProperty("--scroll-fade-content-w", `${Math.round(width)}px`);
      el.style.setProperty("--scroll-fade-content-h", `${Math.round(height)}px`);
    } else {
      el.style.removeProperty("--scroll-fade-content-w");
      el.style.removeProperty("--scroll-fade-content-h");
    }
  };
  const observer = new ResizeObserver(apply);
  const observeChildren = (): void => {
    observer.disconnect();
    observer.observe(el);
    for (const child of el.children) observer.observe(child);
  };
  new MutationObserver(() => {
    observeChildren();
    apply();
  }).observe(el, {childList: true});
  observeChildren();
  apply();
}

export function fitEKStageTeamName(cell: HTMLElement | null | undefined, nameNode: HTMLElement | null | undefined): boolean {
  if (!cell || !nameNode) return false;
  const name = nameNode;
  const baseSize = parseFloat(getComputedStyle(name).fontSize) || 13;
  const minSize = 9;
  const vertOverflows = () => name.scrollHeight > name.clientHeight + 1;
  const horizOverflows = () => isClipped(name);
  name.style.fontSize = "";
  if (vertOverflows()) {
    let size = Math.floor(baseSize) - 1;
    while (size >= minSize) {
      name.style.fontSize = `${size}px`;
      if (!vertOverflows()) break;
      size -= 1;
    }
    if (size < minSize) name.style.fontSize = `${minSize}px`;
  }
  return vertOverflows() || horizOverflows();
}
