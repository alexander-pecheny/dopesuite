// menu.ts — the kit's site-wide chrome entry: theme boot + the ☰ menu
// (Appearance modal, page-supplied jump/extras, account link). Shipped as a
// NON-deferred <head> classic bundle so the stored theme lands on <html>
// before first paint; the menu/modal are built on DOMContentLoaded. The
// decisions live in menu-model.ts; this file is storage + DOM.
import {
  type Contrast,
  type MenuAccount,
  type MenuExtra,
  type MenuItem,
  type MenuJump,
  type ThemePref,
  accountFromMe,
  jumpFromDataset,
  menuItems,
  pickPref,
  resolveTheme,
} from "./menu-model";

const THEME_KEY = "dope-theme";
const CONTRAST_KEY = "dope-contrast";
const root = document.documentElement;

function storage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function readPref<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  let value: string | null = null;
  try {
    value = storage()?.getItem(key) ?? null;
  } catch {}
  return pickPref(value, allowed, fallback);
}

function writePref(key: string, value: string): void {
  try {
    storage()?.setItem(key, value);
  } catch {}
}

let theme: ThemePref = readPref(THEME_KEY, ["light", "dark", "system"], "system");
let contrast: Contrast = readPref(CONTRAST_KEY, ["regular", "high"], "regular");

function prefersDark(): boolean {
  try {
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  } catch {
    return false;
  }
}

// Keep the PWA/browser-chrome colour (meta[name=theme-color], if the app
// injects one) matching the topbar (--structure), so an installed PWA's title
// bar tracks light/dark instead of showing a fixed colour.
function syncThemeColor(): void {
  const meta = document.querySelector('meta[name="theme-color"]');
  if (!meta) return;
  const c = getComputedStyle(root).getPropertyValue("--structure").trim();
  if (c) meta.setAttribute("content", c);
}

function apply(): void {
  root.dataset.theme = resolveTheme(theme, prefersDark());
  root.dataset.contrast = contrast;
  syncThemeColor();
}
apply(); // synchronous — runs during <head> parse, before the body paints
// The meta may be injected after this first apply() (the app's PWA boot runs
// later); re-sync once the DOM is ready so the initial colour is correct too.
document.addEventListener("DOMContentLoaded", syncThemeColor);

try {
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (theme === "system") apply();
  });
} catch {}

let jump: MenuJump | null = null;
let extras: MenuExtra[] = [];
let account: MenuAccount | null = null;
let renderItems: (() => void) | null = null;
let openModalFn: (() => void) | null = null;

// Fetch the signed-in user once so the menu can show a profile link (logged
// in) or a login link (anonymous). Network error = leave the entry out.
function loadAccount(): void {
  fetch("/api/auth/me", { headers: { Accept: "application/json" }, credentials: "same-origin" })
    .then((res) => (res.ok ? res.json().then((data) => accountFromMe(true, data)) : accountFromMe(false, null)))
    .catch(() => null)
    .then((next) => {
      account = next;
      renderItems?.();
    });
}

function setJump(next: MenuJump | null): void {
  jump = next ?? null;
  renderItems?.();
}

function setExtras(items: MenuExtra[]): void {
  extras = Array.isArray(items) ? items.slice() : [];
  renderItems?.();
}

window.dopeMenu = {
  setJump,
  clearJump: () => setJump(null),
  setExtras,
  clearExtras: () => setExtras([]),
  openModal: () => openModalFn?.(),
  get theme() {
    return theme;
  },
  get contrast() {
    return contrast;
  },
};

// Embedded match iframes (?embed=1) are bare — no chrome.
const embedded = ((): boolean => {
  try {
    return new URLSearchParams(location.search).get("embed") === "1";
  } catch {
    return false;
  }
})();

function build(): void {
  if (embedded) return;

  const wrap = document.createElement("div");
  wrap.className = "menu";

  const trigger = document.createElement("button");
  trigger.type = "button";
  trigger.className = "action-icon menu-trigger";
  trigger.setAttribute("aria-label", "Меню");
  trigger.setAttribute("aria-haspopup", "true");
  trigger.setAttribute("aria-expanded", "false");
  // An SVG hamburger centers crisply at any size; the ☰ glyph (U+2630) sits
  // high in its em-box and reads off-centre inside the icon button.
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 18 18");
  svg.setAttribute("width", "18");
  svg.setAttribute("height", "18");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("focusable", "false");
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute("d", "M2.5 5h13M2.5 9h13M2.5 13h13");
  path.setAttribute("stroke", "currentColor");
  path.setAttribute("stroke-width", "1.8");
  path.setAttribute("stroke-linecap", "round");
  svg.append(path);
  trigger.replaceChildren(svg);

  const dropdown = document.createElement("div");
  dropdown.className = "menu-dropdown";
  dropdown.setAttribute("role", "menu");
  dropdown.hidden = true;

  wrap.append(trigger, dropdown);

  // Mount inline in a page header when there is one so the button sits
  // vertically centred in the bar; only truly chrome-less pages float it.
  const actions = document.querySelector(".host-actions");
  const header = document.querySelector(".public-top");
  if (actions) {
    wrap.classList.add("menu-inline");
    actions.appendChild(wrap);
  } else if (header) {
    wrap.classList.add("menu-inline", "menu-public");
    header.appendChild(wrap);
  } else {
    wrap.classList.add("menu-floating");
    document.body.appendChild(wrap);
  }

  function itemNode(item: MenuItem): HTMLElement {
    if (item.kind === "appearance") {
      const appearance = document.createElement("button");
      appearance.type = "button";
      appearance.className = "menu-item";
      appearance.setAttribute("role", "menuitem");
      appearance.textContent = "Оформление";
      appearance.addEventListener("click", () => {
        closeMenu();
        openModal();
      });
      return appearance;
    }
    if (item.kind === "action") {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "menu-item";
      button.setAttribute("role", "menuitem");
      button.textContent = item.label;
      if (item.title) button.title = item.title;
      button.addEventListener("click", () => {
        closeMenu();
        item.onClick();
      });
      return button;
    }
    const link = document.createElement("a");
    link.className = "menu-item";
    link.setAttribute("role", "menuitem");
    link.href = item.href;
    link.textContent = item.label;
    if (item.title) link.title = item.title;
    if (item.external) {
      link.target = "_blank";
      link.rel = "noreferrer";
    }
    if (item.download) link.setAttribute("download", "");
    link.addEventListener("click", closeMenu);
    return link;
  }

  renderItems = function (): void {
    const items = menuItems({ jump, extras, account, config: window.dopeMenuConfig || {} });
    dropdown.replaceChildren(...items.map(itemNode));
  };

  function onOutside(event: Event): void {
    if (!wrap.contains(event.target as Node)) closeMenu();
  }
  function onMenuKey(event: KeyboardEvent): void {
    if (event.key === "Escape") {
      closeMenu();
      trigger.focus();
    }
  }
  function openMenu(): void {
    renderItems?.();
    dropdown.hidden = false;
    trigger.setAttribute("aria-expanded", "true");
    document.addEventListener("pointerdown", onOutside, true);
    document.addEventListener("keydown", onMenuKey);
  }
  function closeMenu(): void {
    dropdown.hidden = true;
    trigger.setAttribute("aria-expanded", "false");
    document.removeEventListener("pointerdown", onOutside, true);
    document.removeEventListener("keydown", onMenuKey);
  }
  trigger.addEventListener("click", () => {
    if (dropdown.hidden) openMenu();
    else closeMenu();
  });

  // ---- Appearance modal ----
  let overlay: HTMLElement | null = null;
  let syncModal: (() => void) | null = null;

  function segmented(
    labelText: string,
    options: Array<{ value: string; label: string }>,
    get: () => string,
    set: (value: string) => void,
  ): { el: HTMLElement; sync: () => void } {
    const row = document.createElement("div");
    row.className = "appearance-row";
    const label = document.createElement("span");
    label.className = "appearance-row-label";
    label.textContent = labelText;
    const group = document.createElement("div");
    group.className = "seg";
    group.setAttribute("role", "group");
    group.setAttribute("aria-label", labelText);
    const buttons = options.map((opt) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "seg-btn";
      button.dataset.value = opt.value;
      button.textContent = opt.label;
      button.addEventListener("click", () => {
        set(opt.value);
        sync();
      });
      group.appendChild(button);
      return button;
    });
    function sync(): void {
      const current = get();
      buttons.forEach((button) => {
        const on = button.dataset.value === current;
        button.classList.toggle("active", on);
        button.setAttribute("aria-pressed", on ? "true" : "false");
      });
    }
    sync();
    row.append(label, group);
    return { el: row, sync };
  }

  function buildModal(): HTMLElement {
    const built = document.createElement("div");
    built.className = "appearance-modal-overlay";
    built.hidden = true;

    const dialog = document.createElement("div");
    dialog.className = "appearance-modal";
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("aria-modal", "true");
    dialog.setAttribute("aria-label", "Оформление");

    const title = document.createElement("h2");
    title.className = "appearance-modal-title";
    title.textContent = "Оформление";

    const themeGroup = segmented(
      "Тема",
      [
        { value: "system", label: "Системная" },
        { value: "light", label: "Светлая" },
        { value: "dark", label: "Тёмная" },
      ],
      () => theme,
      (value) => {
        theme = value as ThemePref;
        writePref(THEME_KEY, value);
        apply();
      },
    );
    const contrastGroup = segmented(
      "Контраст",
      [
        { value: "regular", label: "Обычный" },
        { value: "high", label: "Высокий" },
      ],
      () => contrast,
      (value) => {
        contrast = value as Contrast;
        writePref(CONTRAST_KEY, value);
        apply();
      },
    );

    const done = document.createElement("button");
    done.type = "button";
    done.className = "appearance-modal-done";
    done.textContent = "Готово";
    done.addEventListener("click", closeModal);

    dialog.append(title, themeGroup.el, contrastGroup.el, done);
    built.appendChild(dialog);
    built.addEventListener("pointerdown", (event) => {
      if (event.target === built) closeModal();
    });
    document.body.appendChild(built);
    syncModal = () => {
      themeGroup.sync();
      contrastGroup.sync();
    };
    return built;
  }

  function openModal(): void {
    if (!overlay) overlay = buildModal();
    syncModal?.();
    overlay.hidden = false;
    document.addEventListener("keydown", onModalKey);
    overlay.querySelector<HTMLElement>(".seg-btn")?.focus();
  }
  function closeModal(): void {
    if (overlay) overlay.hidden = true;
    document.removeEventListener("keydown", onModalKey);
  }
  function onModalKey(event: KeyboardEvent): void {
    if (event.key === "Escape") closeModal();
  }

  openModalFn = openModal;
  if (!jump) jump = jumpFromDataset(document.body ? { ...document.body.dataset } : {});
  renderItems();
  loadAccount();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", build);
} else {
  build();
}
