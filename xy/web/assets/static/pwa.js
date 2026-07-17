// pwa.js — xy's PWA boot, loaded on every page after the kit's menu.js:
// inject the manifest/install meta, register the service worker, and disable
// pinch/double-tap zoom (fixed-shell SPA).
(function () {
  "use strict";

  // ---- PWA wiring (runs on every page since this loads everywhere) ----

  // Inject the manifest + install/theme meta into <head> (keeps the HTML files
  // free of per-page boilerplate), then register the service worker. The worker
  // is served from the site root so its scope covers the whole app.
  function injectHeadTag(tag, attrs) {
    for (const sel of attrs.dedupe || []) { if (document.head.querySelector(sel)) return; }
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs.props || {})) node.setAttribute(k, v);
    document.head.appendChild(node);
  }
  try {
    injectHeadTag("link", { dedupe: ['link[rel="manifest"]'], props: { rel: "manifest", href: "/manifest.webmanifest" } });
    // Match the topbar (--structure) rather than a fixed brand colour; menu.js
    // keeps this in sync when the theme flips (fallback = dark topbar).
    const topbar = getComputedStyle(document.documentElement).getPropertyValue("--structure").trim() || "#262a31";
    injectHeadTag("meta", { dedupe: ['meta[name="theme-color"]'], props: { name: "theme-color", content: topbar } });
    injectHeadTag("link", { dedupe: ['link[rel="icon"]'], props: { rel: "icon", type: "image/svg+xml", href: "/static/favicon.svg" } });
    injectHeadTag("meta", { dedupe: ['meta[name="apple-mobile-web-app-capable"]'], props: { name: "apple-mobile-web-app-capable", content: "yes" } });
    injectHeadTag("meta", { dedupe: ['meta[name="apple-mobile-web-app-status-bar-style"]'], props: { name: "apple-mobile-web-app-status-bar-style", content: "default" } });
    injectHeadTag("meta", { dedupe: ['meta[name="apple-mobile-web-app-title"]'], props: { name: "apple-mobile-web-app-title", content: "xy" } });
    injectHeadTag("link", { dedupe: ['link[rel="apple-touch-icon"]'], props: { rel: "apple-touch-icon", href: "/static/apple-touch-icon.png" } });
  } catch (_) {}
  if ("serviceWorker" in navigator) {
    // When a freshly-deployed worker activates and claims the page, reload once so
    // the new app shell (and unversioned modules) take effect immediately instead
    // of on some later visit. Only when a worker was *already* controlling this
    // page (a real update) — never on the first-ever install — and guarded against
    // a reload loop.
    const hadController = !!navigator.serviceWorker.controller;
    let reloading = false;
    navigator.serviceWorker.addEventListener("controllerchange", () => {
      if (reloading || !hadController) return;
      reloading = true;
      location.reload();
    });
    window.addEventListener("load", () => {
      navigator.serviceWorker.register("/sw.js").then((reg) => {
        // Proactively check for a new worker on every load (cheap; the browser
        // byte-compares sw.js), so updates land without waiting for the periodic
        // 24h check.
        reg.update().catch(() => {});
      }).catch(() => {});
    });
  }

  // ---- disable page zoom (every page) ----
  // The app is a fixed-shell SPA; accidental pinch/double-tap zoom (easy to
  // trigger on touch, tedious to undo) just breaks the layout. The viewport meta
  // (maximum-scale=1, user-scalable=no) covers Android/Chrome, but iOS Safari
  // ignores it, so we also cancel the gesture* events and the double-tap and
  // multi-touch pinch it still honours. Single-finger scrolling is untouched.
  try {
    for (const type of ["gesturestart", "gesturechange", "gestureend"]) {
      document.addEventListener(type, (e) => e.preventDefault(), { passive: false });
    }
    document.addEventListener("touchmove", (e) => {
      if (e.touches && e.touches.length > 1) e.preventDefault();
    }, { passive: false });
    let lastTouchEnd = 0;
    document.addEventListener("touchend", (e) => {
      const now = e.timeStamp || 0;
      if (now - lastTouchEnd <= 300) e.preventDefault(); // double-tap zoom
      lastTouchEnd = now;
    }, { passive: false });
  } catch (_) {}
})();
