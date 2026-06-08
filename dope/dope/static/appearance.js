// appearance.js — site-wide theme boot + the ☰ menu (Appearance modal + the
// context-aware "edit / view" jump). Loaded as a NON-deferred <head> script on
// every page so the stored theme is applied to <html> before first paint (no
// flash of the wrong theme). The menu/modal are built on DOMContentLoaded.
//
// Two independent axes, persisted in localStorage and reflected as attributes
// on :root (see styles.css overrides):
//   data-theme    = "light" | "dark"      (default light; we do NOT follow the OS)
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

  let theme = readPref(THEME_KEY, ["light", "dark"], "light");
  let contrast = readPref(CONTRAST_KEY, ["regular", "high"], "regular");

  function apply() {
    root.dataset.theme = theme;
    root.dataset.contrast = contrast;
  }
  apply(); // synchronous — runs during <head> parse, before the body paints

  // The jump item is page-supplied (match-table.js / host.js): on a view-only
  // page with edit rights it links to the editor; on a host page it links to
  // the viewer. Pages without edit context never set it, so their menu shows
  // only "Оформление".
  let jump = null; // {label, href, title, external}
  let account = null; // null until /api/auth/me resolves; then {loggedIn, username}
  let renderItems = null; // wired once the menu is built
  let openModalFn = null;

  // Fetch the signed-in user once so the menu can show a profile link (logged
  // in) or a "Вход для ведущих" link (anonymous), folding in what used to be a
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

  window.dopeAppearance = {
    setJump,
    clearJump: () => setJump(null),
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
    wrap.className = "appearance-menu";

    const trigger = document.createElement("button");
    trigger.type = "button";
    trigger.className = "action-icon appearance-trigger";
    trigger.setAttribute("aria-label", "Меню");
    trigger.setAttribute("aria-haspopup", "true");
    trigger.setAttribute("aria-expanded", "false");
    // An SVG hamburger centers crisply at any size; the ☰ glyph (U+2630) sits
    // high in its em-box and reads off-centre inside the icon button.
    trigger.innerHTML = '<svg viewBox="0 0 18 18" width="18" height="18" aria-hidden="true" focusable="false">'
      + '<path d="M2.5 5h13M2.5 9h13M2.5 13h13" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>';

    const dropdown = document.createElement("div");
    dropdown.className = "appearance-dropdown";
    dropdown.setAttribute("role", "menu");
    dropdown.hidden = true;

    wrap.append(trigger, dropdown);

    const actions = document.querySelector(".host-actions");
    if (actions) {
      wrap.classList.add("appearance-menu-inline");
      actions.appendChild(wrap);
    } else {
      wrap.classList.add("appearance-menu-floating");
      document.body.appendChild(wrap);
    }

    renderItems = function () {
      dropdown.replaceChildren();
      if (jump) {
        const link = document.createElement("a");
        link.className = "appearance-item";
        link.setAttribute("role", "menuitem");
        link.href = jump.href;
        link.textContent = jump.label;
        if (jump.title) link.title = jump.title;
        if (jump.external) { link.target = "_blank"; link.rel = "noreferrer"; }
        link.addEventListener("click", closeMenu);
        dropdown.appendChild(link);
      }
      const appearance = document.createElement("button");
      appearance.type = "button";
      appearance.className = "appearance-item";
      appearance.setAttribute("role", "menuitem");
      appearance.textContent = "Оформление";
      appearance.addEventListener("click", () => { closeMenu(); openModal(); });
      dropdown.appendChild(appearance);

      if (account) {
        const link = document.createElement("a");
        link.className = "appearance-item";
        link.setAttribute("role", "menuitem");
        if (account.loggedIn) {
          link.href = "/profile";
          link.textContent = account.username || "Профиль";
        } else {
          link.href = "/host";
          link.textContent = "Вход для ведущих";
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
      group.className = "appearance-segment";
      group.setAttribute("role", "group");
      group.setAttribute("aria-label", labelText);
      const buttons = options.map((opt) => {
        const button = document.createElement("button");
        button.type = "button";
        button.className = "appearance-segment-btn";
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
          button.classList.toggle("is-active", on);
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
        [{value: "light", label: "Светлая"}, {value: "dark", label: "Тёмная"}],
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
      overlay.querySelector(".appearance-segment-btn")?.focus();
    }
    function closeModal() {
      if (overlay) overlay.hidden = true;
      document.removeEventListener("keydown", onModalKey);
    }
    function onModalKey(event) { if (event.key === "Escape") closeModal(); }

    openModalFn = openModal;
    renderItems();
    loadAccount();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", build);
  } else {
    build();
  }
})();
