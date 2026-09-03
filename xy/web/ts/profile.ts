// profile.ts — username management, logout, and the settings dialogs: change
// password, board sizes (with a pseudo-board preview), default author, card
// title, timezone, and which kind of entry an opened card's feed shows.
import { xyApp, xySizes } from "./app.js";
import { type Modal, modal } from "./modal.js";
import { COMMON_CITIES, guessZone } from "./sessions.js";
import { autocomplete, townChoices, zoneChoices } from "./suggest.js";
import type { AuthMe, Sizes } from "./app.js";

const { fetchJSON, jpost, fetchVoid, el, byId, errMsg } = xyApp;

const whoami = byId("whoami");
const usernameSection = byId("usernameSection");
const usernameForm = byId<HTMLFormElement>("usernameForm");
const usernameMessage = byId("usernameMessage");
const passwordForm = byId<HTMLFormElement>("passwordForm");
const passwordMessage = byId("passwordMessage");

function setText(node: HTMLElement, t: string): void { node.textContent = t; }

// ---- modal plumbing (openBtn → the dialog) ----
// onOpen runs after the overlay unhides (it may need layout — the sizes preview
// measures itself) and may await the /api/auth/me load.
function wireModal(stem: string, openBtnId: string, onOpen?: () => void | Promise<void>): Modal {
  const m = modal(stem);
  byId(openBtnId).addEventListener("click", async () => {
    m.open();
    if (onOpen) await onOpen();
  });
  return m;
}

// ---- state loaded from /api/auth/me ----
let sizes: Sizes = { ...xySizes.DEFAULT };
let defaultAuthor = "";
let cardTitle = "question"; // which field a card's board preview shows
let feedDefault = "all"; // which kind of entry an opened card's feed shows
let timezone = "";
let announceCities: Array<{ zone: string; name: string }> = [];
let sessionTitleMode = "date-title";

async function boot(): Promise<void> {
  const me = await xyApp.requireLogin();
  if (!me) return;
  const m: Partial<AuthMe> & { telegram?: string | null } = "user_id" in me ? me : {};
  whoami.textContent = m.username || m.telegram || ("#" + m.user_id);
  if (!m.username) usernameSection.hidden = false;
  sizes = xySizes.sanitize(m.sizes);
  defaultAuthor = m.default_author || "";
  cardTitle = m.card_title || "question";
  feedDefault = m.feed_default || "all";
  timezone = m.timezone || "";
  announceCities = Array.isArray(m.announce_cities) ? (m.announce_cities as Array<{ zone: string; name: string }>) : [];
  sessionTitleMode = m.session_title_mode || "date-title";
  loadStorage();
}
const booted = boot();

const MB = 1024 * 1024;
function fmtMB(bytes: number): string { return (bytes / MB).toFixed(1) + " МБ"; }

async function loadStorage(): Promise<void> {
  const node = byId("storageUsed");
  try {
    const s = (await fetchJSON("/api/auth/storage")) as { unlimited?: boolean; used_bytes: number; quota_bytes: number };
    node.textContent = s.unlimited
      ? fmtMB(s.used_bytes) + " (без лимита)"
      : fmtMB(s.used_bytes) + " из " + fmtMB(s.quota_bytes);
  } catch (_) {
    node.textContent = "—";
  }
}

usernameForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  setText(usernameMessage, "");
  try {
    await jpost("/api/auth/username", { username: byId<HTMLInputElement>("usernameValue").value.trim() });
    window.location.reload();
  } catch (err) {
    setText(usernameMessage, errMsg(err));
  }
});

// ---- change password ----
wireModal("password", "passwordBtn", () => {
  passwordForm.reset();
  setText(passwordMessage, "");
});

passwordForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  setText(passwordMessage, "");
  const newPassword = byId<HTMLInputElement>("newPassword").value;
  const confirm = byId<HTMLInputElement>("confirmPassword").value;
  if (newPassword !== confirm) {
    setText(passwordMessage, "Пароли не совпадают.");
    return;
  }
  const body: { new_password: string; current_password?: string } = { new_password: newPassword };
  const cur = byId<HTMLInputElement>("currentPassword").value;
  if (cur) body.current_password = cur;
  try {
    await jpost("/api/auth/password", body);
    passwordForm.reset();
    setText(passwordMessage, "Пароль сохранён.");
  } catch (err) {
    setText(passwordMessage, errMsg(err));
  }
});

// ---- board sizes (workspace width / list width / card height) ----
// The values live in users.sizes (see xySizes in app.ts for defaults/ranges);
// there is no board on this page, so the effect is shown on a pseudo-board:
// a to-scale wireframe of a monitor with lists of "text line" bars.

const sizesBoardW = byId<HTMLInputElement>("sizesBoardW");
const sizesListW = byId<HTMLInputElement>("sizesListW");
const sizesCardH = byId<HTMLInputElement>("sizesCardH");
const sizesCardFont = byId<HTMLInputElement>("sizesCardFont");
const preview = byId("sizesPreview");

// The pretend monitor the preview scales against: wide enough that the default
// 1512px board visibly centres, and "full width" visibly fills.
const PREVIEW_SCREEN_W = 2000;
// Fake cards, as question lengths in text lines — varied so the card-height
// clamp visibly cuts some cards and not others.
const PREVIEW_CARDS = [3, 6, 1, 9, 2, 4, 7, 2];

function renderPreview(): void {
  const k = (preview.clientWidth || 360) / PREVIEW_SCREEN_W;
  preview.style.setProperty("--pv-board-w", sizes.boardW == null ? "none" : Math.round(sizes.boardW * k) + "px");
  preview.style.setProperty("--pv-list-w", Math.round(sizes.listW * k) + "px");
  // A text line is ~1.4× the font size; scale it like everything else so the
  // font knob visibly re-packs the wireframe cards.
  preview.style.setProperty("--pvb-line-h", Math.max(1.5, sizes.cardFont * 1.4 * k).toFixed(1) + "px");
  const lists = [];
  for (let i = 0; i < 8; i++) {
    const cards = [];
    for (let j = 0; j < 3; j++) {
      const total = PREVIEW_CARDS[(i + j * 3) % PREVIEW_CARDS.length];
      const shown = sizes.cardLines == null ? total : Math.min(total, sizes.cardLines);
      const bars = [];
      for (let n = 0; n < shown; n++) {
        bars.push(el("div", { class: "pvb-line" + (n === shown - 1 ? " pvb-line-last" : "") }));
      }
      cards.push(el("div", { class: "pvb-card" }, bars));
    }
    lists.push(el("div", { class: "pvb-list" }, el("div", { class: "pvb-title" }), cards));
  }
  preview.replaceChildren(el("div", { class: "pvb-screen" }, el("div", { class: "pvb-board" }, lists)));
}

function syncSizesUI(): void {
  const s = sizes;
  sizesBoardW.value = String(s.boardW == null ? xySizes.BOARD_W_MAX : s.boardW);
  sizesListW.value = String(s.listW);
  sizesCardH.value = String(s.cardLines == null ? xySizes.CARD_LINES_MAX : s.cardLines);
  sizesCardFont.value = String(s.cardFont);
  byId("sizesBoardWVal").textContent = s.boardW == null ? "вся ширина" : s.boardW + " px";
  byId("sizesListWVal").textContent = s.listW + " px";
  byId("sizesCardHVal").textContent =
    s.cardLines == null ? "весь текст" : s.cardLines + (s.cardLines === 1 ? " строка" : s.cardLines < 5 ? " строки" : " строк");
  byId("sizesCardFontVal").textContent = s.cardFont + " px";
  renderPreview();
}

// Debounce the save so dragging a slider fires one request, not one per pixel.
let sizesSaveTimer: number | null = null;
function scheduleSizesSave(): void {
  if (sizesSaveTimer) clearTimeout(sizesSaveTimer);
  sizesSaveTimer = setTimeout(async () => {
    sizesSaveTimer = null;
    // Best-effort — the sliders already show the value, a failed save is not fatal.
    try { await jpost("/api/auth/sizes", sizes); } catch (_) {}
  }, 400);
}

function commitSizes(): void {
  const boardW = Number(sizesBoardW.value), lines = Number(sizesCardH.value);
  sizes = {
    boardW: boardW >= xySizes.BOARD_W_MAX ? null : boardW,
    listW: Number(sizesListW.value),
    cardLines: lines >= xySizes.CARD_LINES_MAX ? null : lines,
    cardFont: Number(sizesCardFont.value),
  };
  syncSizesUI();
  scheduleSizesSave();
}

sizesBoardW.addEventListener("input", commitSizes);
sizesListW.addEventListener("input", commitSizes);
sizesCardH.addEventListener("input", commitSizes);
sizesCardFont.addEventListener("input", commitSizes);
byId("sizesReset").addEventListener("click", () => {
  sizes = { ...xySizes.DEFAULT };
  syncSizesUI();
  scheduleSizesSave();
});

wireModal("sizes", "sizesBtn", async () => {
  await booted;
  syncSizesUI();
});

// ---- default author ----
const authorForm = byId<HTMLFormElement>("authorForm");
const authorMessage = byId("authorMessage");
const authorModal = wireModal("author", "authorBtn", async () => {
  await booted;
  byId<HTMLInputElement>("authorValue").value = defaultAuthor;
  setText(authorMessage, "");
});

authorForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  setText(authorMessage, "");
  const v = byId<HTMLInputElement>("authorValue").value.trim();
  try {
    await jpost("/api/auth/default-author", { default_author: v });
    defaultAuthor = v;
    authorModal.close();
  } catch (err) {
    setText(authorMessage, errMsg(err));
  }
});

// ---- timezone, announce cities, session label naming ----
// The timezone does two jobs and neither is rendering: it is the zone a new test
// session's time is written in, and the first city of its announce set. The
// cities are only the seed — a session keeps its own copy, because who is
// invited changes from test to test.
const tzForm = byId<HTMLFormElement>("tzForm");
const tzMessage = byId("tzMessage");
const tzModal = wireModal("tz", "tzBtn", async () => {
  await booted;
  byId<HTMLInputElement>("tzValue").value = timezone || guessZone();
  byId<HTMLInputElement>("tzCities").value = announceCities.map((c) => c.name).join(", ");
  byId<HTMLSelectElement>("tzTitleMode").value = sessionTitleMode;
  setText(tzMessage, "");
});

// citiesFromNames resolves typed names against the built-in table; an unknown one
// is kept with the caller's own zone, so the invite line still names it.
function citiesFromNames(raw: string, ownZone: string): Array<{ zone: string; name: string }> {
  return raw.split(",").map((s) => s.trim()).filter(Boolean).map((name) => {
    const known = COMMON_CITIES.find((c) => c.name.toLowerCase() === name.toLowerCase());
    return known || { zone: ownZone, name };
  });
}

// The same pickers the session form uses: a bare box here meant typing an IANA
// id from memory, and "almaty" finding nothing.
autocomplete(byId<HTMLInputElement>("tzValue"), zoneChoices);
autocomplete(byId<HTMLInputElement>("tzCities"), (q) => {
  // The field is a comma-separated list, so complete only its LAST entry.
  const head = q.slice(0, q.lastIndexOf(",") + 1);
  const tail = q.slice(q.lastIndexOf(",") + 1).trim();
  if (!tail) return [];
  return townChoices(tail).map((c) => ({ ...c, value: head + (head ? " " : "") + c.value }));
});

tzForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  setText(tzMessage, "");
  const tz = byId<HTMLInputElement>("tzValue").value.trim();
  const mode = byId<HTMLSelectElement>("tzTitleMode").value;
  const cities = citiesFromNames(byId<HTMLInputElement>("tzCities").value, tz || guessZone());
  try {
    await jpost("/api/auth/profile-defaults", { timezone: tz, session_title_mode: mode });
    await jpost("/api/auth/announce-cities", { announce_cities: cities });
    timezone = tz;
    sessionTitleMode = mode;
    announceCities = cities;
    tzModal.close();
  } catch (err) {
    setText(tzMessage, errMsg(err));
  }
});

// ---- card title (question text vs answer) ----
const cardTitleForm = byId<HTMLFormElement>("cardTitleForm");
const cardTitleMessage = byId("cardTitleMessage");
const cardTitleRadios = () => cardTitleForm.querySelectorAll<HTMLInputElement>('input[name="cardTitle"]');
const cardTitleModal = wireModal("cardTitle", "cardTitleBtn", async () => {
  await booted;
  for (const r of cardTitleRadios()) r.checked = r.value === cardTitle;
  setText(cardTitleMessage, "");
});

cardTitleForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  setText(cardTitleMessage, "");
  const picked = [...cardTitleRadios()].find((r) => r.checked);
  const v = picked ? picked.value : "question";
  try {
    await jpost("/api/auth/card-title", { card_title: v });
    cardTitle = v;
    cardTitleModal.close();
  } catch (err) {
    setText(cardTitleMessage, errMsg(err));
  }
});

// ---- feed (which kind of entry an opened card's feed shows) ----
const feedDefaultForm = byId<HTMLFormElement>("feedDefaultForm");
const feedDefaultMessage = byId("feedDefaultMessage");
const feedDefaultRadios = () => feedDefaultForm.querySelectorAll<HTMLInputElement>('input[name="feedDefault"]');
const feedDefaultModal = wireModal("feedDefault", "feedDefaultBtn", async () => {
  await booted;
  for (const r of feedDefaultRadios()) r.checked = r.value === feedDefault;
  setText(feedDefaultMessage, "");
});

feedDefaultForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  setText(feedDefaultMessage, "");
  const picked = [...feedDefaultRadios()].find((r) => r.checked);
  const v = picked ? picked.value : "all";
  try {
    await jpost("/api/auth/feed-default", { feed_default: v });
    feedDefault = v;
    feedDefaultModal.close();
  } catch (err) {
    setText(feedDefaultMessage, errMsg(err));
  }
});

byId("logoutBtn").addEventListener("click", async () => {
  try { await fetchVoid("/api/auth/logout", { method: "POST" }); } catch (_) {}
  window.location.replace("/login");
});
