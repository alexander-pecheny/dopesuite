// profile.ts — username management, logout, the settings shown on the page
// (interface font, card title, which kind of entry an opened card's feed
// shows, default author, timezone and invite cities), and the two dialogs:
// change password and board sizes (with a pseudo-board preview).
import S from "./i18nstrings.js";
import { xyApp, xySizes } from "./app.js";
import { type Modal, modal } from "./modal.js";
import { COMMON_CITIES, guessZone } from "./sessions.js";
import { autocomplete } from "./kit/suggest.js";
import { townChoices, zoneChoices } from "./suggest.js";
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
let uiFont = "noto"; // the body face the whole site is set in (users.ui_font)
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
  uiFont = m.ui_font || "noto";
  timezone = m.timezone || "";
  announceCities = Array.isArray(m.announce_cities) ? (m.announce_cities as Array<{ zone: string; name: string }>) : [];
  sessionTitleMode = m.session_title_mode || "date-title";
  loadStorage();
}
const booted = boot();

const MB = 1024 * 1024;
function fmtMB(bytes: number): string { return S.profile.storage.used((bytes / MB).toFixed(1)); }

async function loadStorage(): Promise<void> {
  const node = byId("storageUsed");
  try {
    const s = (await fetchJSON("/api/auth/storage")) as { unlimited?: boolean; used_bytes: number; quota_bytes: number };
    node.textContent = s.unlimited
      ? S.profile.storage.unlimited(fmtMB(s.used_bytes))
      : S.profile.storage.ofQuota(fmtMB(s.used_bytes), fmtMB(s.quota_bytes));
  } catch (_) {
    node.textContent = S.profile.storage.unknown();
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
    setText(passwordMessage, S.profile.password.mismatch());
    return;
  }
  const body: { new_password: string; current_password?: string } = { new_password: newPassword };
  const cur = byId<HTMLInputElement>("currentPassword").value;
  if (cur) body.current_password = cur;
  try {
    await jpost("/api/auth/password", body);
    passwordForm.reset();
    setText(passwordMessage, S.profile.password.saved());
  } catch (err) {
    setText(passwordMessage, errMsg(err));
  }
});

// ---- board sizes (workspace width / list width / card height) ----
// The values live in users.sizes (see xySizes in app.ts for defaults/ranges);
// there is no board on this page, so the effect is shown on a pseudo-board:
// a to-scale wireframe of a monitor with lists of "text line" bars.

const sizesBoardW = byId<HTMLInputElement>("sizesBoardW");
const sizesBoardGrow = byId<HTMLInputElement>("sizesBoardGrow");
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
// Few enough lists that they fit inside the default width, so the slider has
// something to move: past the point where they stop fitting, a board with
// boardGrow on takes the whole screen and the width no longer decides anything
// (.kanban in styles.css).
const PREVIEW_LISTS = 4;
const PREVIEW_CARDS_PER_LIST = 3;
// The preview's width before it has been laid out.
const PREVIEW_FALLBACK_W = 360;
const LINE_HEIGHT_RATIO = 1.4; // a text line is ~1.4× the font size
const MIN_LINE_PX = 1.5; // a wireframe bar never gets thinner than this

function renderPreview(): void {
  const k = (preview.clientWidth || PREVIEW_FALLBACK_W) / PREVIEW_SCREEN_W;
  preview.style.setProperty("--pv-board-w", sizes.boardW == null ? "none" : Math.round(sizes.boardW * k) + "px");
  preview.style.setProperty("--pv-list-w", Math.round(sizes.listW * k) + "px");
  preview.style.setProperty("--pv-list-count", String(PREVIEW_LISTS));
  preview.style.setProperty("--kanban-grow", sizes.boardGrow ? "1" : "0");
  // Scale the line height like everything else so the font knob visibly
  // re-packs the wireframe cards.
  preview.style.setProperty("--pvb-line-h", Math.max(MIN_LINE_PX, sizes.cardFont * LINE_HEIGHT_RATIO * k).toFixed(1) + "px");
  const lists = [];
  for (let i = 0; i < PREVIEW_LISTS; i++) {
    const cards = [];
    for (let j = 0; j < PREVIEW_CARDS_PER_LIST; j++) {
      const total = PREVIEW_CARDS[(i + j * PREVIEW_CARDS_PER_LIST) % PREVIEW_CARDS.length];
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
  sizesBoardGrow.checked = s.boardGrow;
  sizesListW.value = String(s.listW);
  sizesCardH.value = String(s.cardLines == null ? xySizes.CARD_LINES_MAX : s.cardLines);
  sizesCardFont.value = String(s.cardFont);
  byId("sizesBoardWVal").textContent = s.boardW == null ? S.profile.sizes.boardWMax() : S.profile.sizes.px(String(s.boardW));
  byId("sizesListWVal").textContent = S.profile.sizes.px(String(s.listW));
  byId("sizesCardHVal").textContent =
    s.cardLines == null ? S.profile.sizes.cardHMax() : S.profile.sizes.cardLines(s.cardLines);
  byId("sizesCardFontVal").textContent = S.profile.sizes.px(String(s.cardFont));
  renderPreview();
}

// Debounce the save so dragging a slider fires one request, not one per pixel.
const SIZES_SAVE_DELAY_MS = 400;
let sizesSaveTimer: number | null = null;
function scheduleSizesSave(): void {
  if (sizesSaveTimer) clearTimeout(sizesSaveTimer);
  sizesSaveTimer = setTimeout(async () => {
    sizesSaveTimer = null;
    // Best-effort — the sliders already show the value, a failed save is not fatal.
    try { await jpost("/api/auth/sizes", sizes); } catch (_) {}
  }, SIZES_SAVE_DELAY_MS);
}

function commitSizes(): void {
  const boardW = Number(sizesBoardW.value), lines = Number(sizesCardH.value);
  sizes = {
    boardW: boardW >= xySizes.BOARD_W_MAX ? null : boardW,
    boardGrow: sizesBoardGrow.checked,
    listW: Number(sizesListW.value),
    cardLines: lines >= xySizes.CARD_LINES_MAX ? null : lines,
    cardFont: Number(sizesCardFont.value),
  };
  syncSizesUI();
  scheduleSizesSave();
}

sizesBoardW.addEventListener("input", commitSizes);
sizesBoardGrow.addEventListener("change", commitSizes);
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

// ---- the settings shown on the page ----
// Each control shows the account's current value and saves when it changes.
// Its message line says it saved, or why the save failed; a text field also
// saves while you pause typing, since a suggestion picked from its list fires
// no change event.
const FLASH_MS = 2000; // how long "saved" stays up
// A typed field saves after this long without a keystroke.
const TYPING_SAVE_DELAY_MS = 800;

function flash(node: HTMLElement, text: string): void {
  setText(node, text);
  if (!text) return;
  const shown = text;
  setTimeout(() => { if (node.textContent === shown) setText(node, ""); }, FLASH_MS);
}

// saveOn wires one control: `save` posts it and throws on failure, `revert`
// puts the control back to what the account still holds.
function saveOn(controls: HTMLElement[], message: HTMLElement, save: () => Promise<void>, revert: () => void, typing = false): void {
  let timer: number | null = null;
  const run = async (): Promise<void> => {
    if (timer) { clearTimeout(timer); timer = null; }
    setText(message, "");
    try {
      await save();
      flash(message, S.profile.saved());
    } catch (err) {
      revert();
      setText(message, errMsg(err));
    }
  };
  for (const c of controls) {
    c.addEventListener("change", () => { void run(); });
    if (typing) c.addEventListener("input", () => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => { void run(); }, TYPING_SAVE_DELAY_MS);
    });
  }
}

// Interface font. Two halves, and they are not the same thing: users.ui_font is
// where the choice LIVES (it follows the reader to any device), and the kit's
// chrome is what APPLIES it, on <html> before first paint, on every page. So a
// pick does both: the chrome at once, since the page itself is the preview, and
// the POST behind it. The face is only downloaded once it is picked.
const fontSelect = byId<HTMLSelectElement>("uiFont");
saveOn([fontSelect], byId("fontMessage"), async () => {
  window.dopeMenu?.setFont(fontSelect.value);
  await jpost("/api/auth/ui-font", { ui_font: fontSelect.value });
  uiFont = fontSelect.value;
}, () => {
  fontSelect.value = uiFont;
  window.dopeMenu?.setFont(uiFont);
});

const cardTitleSelect = byId<HTMLSelectElement>("cardTitle");
saveOn([cardTitleSelect], byId("cardTitleMessage"), async () => {
  await jpost("/api/auth/card-title", { card_title: cardTitleSelect.value });
  cardTitle = cardTitleSelect.value;
}, () => { cardTitleSelect.value = cardTitle; });

const feedSelect = byId<HTMLSelectElement>("feedDefault");
saveOn([feedSelect], byId("feedDefaultMessage"), async () => {
  await jpost("/api/auth/feed-default", { feed_default: feedSelect.value });
  feedDefault = feedSelect.value;
}, () => { feedSelect.value = feedDefault; });

const authorInput = byId<HTMLInputElement>("authorValue");
saveOn([authorInput], byId("authorMessage"), async () => {
  const v = authorInput.value.trim();
  if (v === defaultAuthor) return;
  await jpost("/api/auth/default-author", { default_author: v });
  defaultAuthor = v;
}, () => { authorInput.value = defaultAuthor; }, true);

// Timezone, announce cities, session label naming. The timezone does two jobs
// and neither is rendering: it is the zone a new test session's time is written
// in, and the first city of its announce set. The cities are only the seed — a
// session keeps its own copy, because who is invited changes from test to test.
const tzInput = byId<HTMLInputElement>("tzValue");
const citiesInput = byId<HTMLInputElement>("tzCities");
const titleModeSelect = byId<HTMLSelectElement>("tzTitleMode");

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
autocomplete(tzInput, zoneChoices);
autocomplete(citiesInput, (q) => {
  // The field is a comma-separated list, so complete only its LAST entry.
  const head = q.slice(0, q.lastIndexOf(",") + 1);
  const tail = q.slice(q.lastIndexOf(",") + 1).trim();
  if (!tail) return [];
  return townChoices(tail).map((c) => ({ ...c, value: head + (head ? " " : "") + c.value }));
});

const cityNames = (cities: Array<{ name: string }>): string => cities.map((c) => c.name).join(", ");

saveOn([tzInput, citiesInput, titleModeSelect], byId("tzMessage"), async () => {
  const tz = tzInput.value.trim();
  const mode = titleModeSelect.value;
  const cities = citiesFromNames(citiesInput.value, tz || guessZone());
  if (tz === timezone && mode === sessionTitleMode && cityNames(cities) === cityNames(announceCities)) return;
  await jpost("/api/auth/profile-defaults", { timezone: tz, session_title_mode: mode });
  await jpost("/api/auth/announce-cities", { announce_cities: cities });
  timezone = tz;
  sessionTitleMode = mode;
  announceCities = cities;
}, () => {
  tzInput.value = timezone;
  citiesInput.value = cityNames(announceCities);
  titleModeSelect.value = sessionTitleMode;
}, true);

// Show what the account holds, once /api/auth/me has answered.
void booted.then(() => {
  fontSelect.value = uiFont;
  cardTitleSelect.value = cardTitle;
  feedSelect.value = feedDefault;
  authorInput.value = defaultAuthor;
  tzInput.value = timezone || guessZone();
  citiesInput.value = cityNames(announceCities);
  titleModeSelect.value = sessionTitleMode;
});

byId("logoutBtn").addEventListener("click", async () => {
  try { await fetchVoid("/api/auth/logout", { method: "POST" }); } catch (_) {}
  window.location.replace("/login");
});
