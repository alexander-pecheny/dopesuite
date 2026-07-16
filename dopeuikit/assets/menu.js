// menu.js — the kit's site-wide chrome script: theme boot + the ☰ menu
// (Appearance modal + the context-aware "edit / view" jump, page-supplied
// download links, account link). Loaded as a NON-deferred <head> script on
// every page so the stored theme is applied to <html> before first paint (no
// flash of the wrong theme). The menu/modal are built on DOMContentLoaded.
// App-specific menu labels come from window.dopeMenuConfig (see the account
// entry); app-specific page behaviour (xy's PWA boot) lives in the app's own
// boot script, not here.
//
// Two independent axes, persisted in localStorage and reflected as attributes
// on :root (see styles.css overrides):
//   data-theme    = "light" | "dark"      (resolved from "system"|"light"|"dark"; default system)
//   data-contrast = "regular" | "high"    (default regular)
(function () {
  "use strict";

  const THEME_KEY = "dope-theme";
  const CONTRAST_KEY = "dope-contrast";
  const root = document.documentElement;

  function storage() {
    try { return window.localStorage; } catch (_) { return null; }
  }
  function readPref(key, allowed, fallback) {
    const s = storage();
    let value = null;
    if (s) { try { value = s.getItem(key); } catch (_) {} }
    return allowed.includes(value) ? value : fallback;
  }
  function writePref(key, value) {
    const s = storage();
    if (s) { try { s.setItem(key, value); } catch (_) {} }
  }

  let theme = readPref(THEME_KEY, ["light", "dark", "system"], "system");
  let contrast = readPref(CONTRAST_KEY, ["regular", "high"], "regular");

  function resolveTheme() {
    if (theme !== "system") return theme;
    try { return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"; }
    catch (_) { return "light"; }
  }

  function apply() {
    root.dataset.theme = resolveTheme();
    root.dataset.contrast = contrast;
  }
  apply(); // synchronous — runs during <head> parse, before the body paints

  // Re-apply when OS preference changes while "system" mode is active.
  try {
    window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
      if (theme === "system") apply();
    });
  } catch (_) {}

  // The jump item is page-supplied (match-table.js / host.js): on a view-only
  // page with edit rights it links to the editor; on a host page it links to
  // the viewer. Pages without edit context never set it, so their menu shows
  // only "Оформление".
  let jump = null; // {label, href, title, external}
  let extras = []; // page-supplied action links, e.g. downloads: [{label, href, title, download}]
  let account = null; // null until /api/auth/me resolves; then {loggedIn, username}
  let renderItems = null; // wired once the menu is built
  let openModalFn = null;

  // Fetch the signed-in user once so the menu can show a profile link (logged
  // in) or a "Вход для ведущего" link (anonymous), folding in what used to be a
  // separate corner link. 401 = anonymous; network error = leave it out.
  function loadAccount() {
    fetch("/api/auth/me", {headers: {Accept: "application/json"}, credentials: "same-origin"})
      .then((res) => {
        if (!res.ok) return {loggedIn: false};
        return res.json().then((data) => ({
          loggedIn: true,
          username: (data && (data.username || data.telegram)) || null,
        }));
      })
      .catch(() => null)
      .then((next) => { account = next; renderItems?.(); });
  }

  function setJump(next) {
    jump = next || null;
    renderItems?.();
  }

  function setExtras(items) {
    extras = Array.isArray(items) ? items.slice() : [];
    renderItems?.();
  }

  window.dopeMenu = {
    setJump,
    clearJump: () => setJump(null),
    setExtras,
    clearExtras: () => setExtras([]),
    openModal: () => openModalFn?.(),
    get theme() { return theme; },
    get contrast() { return contrast; },
  };

  // Embedded match iframes (?embed=1) are bare — no chrome.
  const embedded = (() => {
    try { return new URLSearchParams(location.search).get("embed") === "1"; }
    catch (_) { return false; }
  })();

  function build() {
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

    renderItems = function () {
      dropdown.replaceChildren();
      // Order: Оформление, then the page-supplied jump (Редактировать / Страница
      // зрителя), then the account link (Вход для ведущего / Профиль ведущего).
      const appearance = document.createElement("button");
      appearance.type = "button";
      appearance.className = "menu-item";
      appearance.setAttribute("role", "menuitem");
      appearance.textContent = "Оформление";
      appearance.addEventListener("click", () => { closeMenu(); openModal(); });
      dropdown.appendChild(appearance);

      if (jump) {
        const link = document.createElement("a");
        link.className = "menu-item";
        link.setAttribute("role", "menuitem");
        link.href = jump.href;
        link.textContent = jump.label;
        if (jump.title) link.title = jump.title;
        if (jump.external) { link.target = "_blank"; link.rel = "noreferrer"; }
        link.addEventListener("click", closeMenu);
        dropdown.appendChild(link);
      }

      for (const item of extras) {
        // Action items (with onClick) render as a <button>; link items as an <a>.
        const node = document.createElement(item.onClick ? "button" : "a");
        node.className = "menu-item";
        node.setAttribute("role", "menuitem");
        if (item.onClick) {
          node.type = "button";
          node.addEventListener("click", () => { closeMenu(); item.onClick(); });
        } else {
          node.href = item.href;
          if (item.download) node.setAttribute("download", "");
          node.addEventListener("click", closeMenu);
        }
        node.textContent = item.label;
        if (item.title) node.title = item.title;
        dropdown.appendChild(node);
      }

      if (account) {
        // Labels/targets are the app's (dope's account entry is the host's
        // door): window.dopeMenuConfig, set by a boot script loaded before us.
        const cfg = window.dopeMenuConfig || {};
        const link = document.createElement("a");
        link.className = "menu-item";
        link.setAttribute("role", "menuitem");
        if (account.loggedIn) {
          link.href = cfg.profileHref || "/profile";
          link.textContent = cfg.profileLabel || "Профиль";
        } else {
          link.href = cfg.loginHref || "/login";
          link.textContent = cfg.loginLabel || "Вход";
        }
        link.addEventListener("click", closeMenu);
        dropdown.appendChild(link);
      }
    };

    function openMenu() {
      renderItems();
      dropdown.hidden = false;
      trigger.setAttribute("aria-expanded", "true");
      document.addEventListener("pointerdown", onOutside, true);
      document.addEventListener("keydown", onMenuKey);
    }
    function closeMenu() {
      dropdown.hidden = true;
      trigger.setAttribute("aria-expanded", "false");
      document.removeEventListener("pointerdown", onOutside, true);
      document.removeEventListener("keydown", onMenuKey);
    }
    function onOutside(event) { if (!wrap.contains(event.target)) closeMenu(); }
    function onMenuKey(event) {
      if (event.key === "Escape") { closeMenu(); trigger.focus(); }
    }
    trigger.addEventListener("click", () => {
      if (dropdown.hidden) openMenu(); else closeMenu();
    });

    // ---- Appearance modal ----
    let overlay = null;
    let syncModal = null;

    function segmented(labelText, options, get, set) {
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
        button.addEventListener("click", () => { set(opt.value); sync(); });
        group.appendChild(button);
        return button;
      });
      function sync() {
        const current = get();
        buttons.forEach((button) => {
          const on = button.dataset.value === current;
          button.classList.toggle("active", on);
          button.setAttribute("aria-pressed", on ? "true" : "false");
        });
      }
      sync();
      row.append(label, group);
      return {el: row, sync};
    }

    function buildModal() {
      overlay = document.createElement("div");
      overlay.className = "appearance-modal-overlay";
      overlay.hidden = true;

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
        [{value: "system", label: "Системная"}, {value: "light", label: "Светлая"}, {value: "dark", label: "Тёмная"}],
        () => theme,
        (value) => { theme = value; writePref(THEME_KEY, value); apply(); },
      );
      const contrastGroup = segmented(
        "Контраст",
        [{value: "regular", label: "Обычный"}, {value: "high", label: "Высокий"}],
        () => contrast,
        (value) => { contrast = value; writePref(CONTRAST_KEY, value); apply(); },
      );

      const done = document.createElement("button");
      done.type = "button";
      done.className = "appearance-modal-done";
      done.textContent = "Готово";
      done.addEventListener("click", closeModal);

      dialog.append(title, themeGroup.el, contrastGroup.el, done);
      overlay.appendChild(dialog);
      overlay.addEventListener("pointerdown", (event) => {
        if (event.target === overlay) closeModal();
      });
      document.body.appendChild(overlay);
      syncModal = () => { themeGroup.sync(); contrastGroup.sync(); };
    }

    function openModal() {
      if (!overlay) buildModal();
      syncModal();
      overlay.hidden = false;
      document.addEventListener("keydown", onModalKey);
      overlay.querySelector(".seg-btn")?.focus();
    }
    function closeModal() {
      if (overlay) overlay.hidden = true;
      document.removeEventListener("keydown", onModalKey);
    }
    function onModalKey(event) { if (event.key === "Escape") closeModal(); }

    openModalFn = openModal;
    // Server-rendered pages with no JS of their own (home, fest overviews)
    // declare their host/viewer jump statically via body data-jump-* attributes.
    // Honour it unless a page script already supplied one.
    if (!jump) {
      const d = document.body ? document.body.dataset : {};
      if (d.jumpHref) {
        jump = {
          label: d.jumpLabel || "Перейти",
          href: d.jumpHref,
          title: d.jumpTitle || "",
          external: d.jumpExternal === "1",
        };
      }
    }
    renderItems();
    loadAccount();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", build);
  } else {
    build();
  }
})();
