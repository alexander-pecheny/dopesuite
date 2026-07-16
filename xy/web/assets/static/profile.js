// profile.js — username management, logout, and the three settings dialogs:
// change password, board sizes (with a pseudo-board preview), default author.
import { xyApp, xySizes } from "./app.js";

const { fetchJSON, jpost, fetchVoid, el } = xyApp;

const whoami = document.getElementById("whoami");
const usernameSection = document.getElementById("usernameSection");
const usernameForm = document.getElementById("usernameForm");
const usernameMessage = document.getElementById("usernameMessage");
const passwordForm = document.getElementById("passwordForm");
const passwordMessage = document.getElementById("passwordMessage");

function setText(node, t) { node.textContent = t; }

// ---- modal plumbing (openBtn → overlay; close on Отмена, backdrop, Escape) ----
// onOpen runs after the overlay unhides (it may need layout — the sizes preview
// measures itself) and may await the /api/auth/me load.
function wireModal(overlayId, openBtnId, cancelBtnId, onOpen) {
  const overlay = document.getElementById(overlayId);
  document.getElementById(openBtnId).addEventListener("click", async () => {
    overlay.hidden = false;
    if (onOpen) await onOpen();
  });
  const close = () => { overlay.hidden = true; };
  if (cancelBtnId) document.getElementById(cancelBtnId).addEventListener("click", close);
  overlay.addEventListener("click", (e) => { if (e.target === overlay) close(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !overlay.hidden) close(); });
  return { overlay, close };
}

// ---- state loaded from /api/auth/me ----
let sizes = { ...xySizes.DEFAULT };
let defaultAuthor = "";

async function boot() {
  const me = await xyApp.requireLogin();
  if (!me) return;
  whoami.textContent = me.username || me.telegram || ("#" + me.user_id);
  if (!me.username) usernameSection.hidden = false;
  sizes = xySizes.sanitize(me.sizes);
  defaultAuthor = me.default_author || "";
}
const booted = boot();

usernameForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  setText(usernameMessage, "");
  try {
    await jpost("/api/auth/username", { username: document.getElementById("usernameValue").value.trim() });
    window.location.reload();
  } catch (err) {
    setText(usernameMessage, err.message);
  }
});

// ---- change password ----
wireModal("passwordOverlay", "passwordBtn", "passwordCancel", () => {
  passwordForm.reset();
  setText(passwordMessage, "");
});

passwordForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  setText(passwordMessage, "");
  const newPassword = document.getElementById("newPassword").value;
  const confirm = document.getElementById("confirmPassword").value;
  if (newPassword !== confirm) {
    setText(passwordMessage, "Пароли не совпадают.");
    return;
  }
  const body = { new_password: newPassword };
  const cur = document.getElementById("currentPassword").value;
  if (cur) body.current_password = cur;
  try {
    await jpost("/api/auth/password", body);
    passwordForm.reset();
    setText(passwordMessage, "Пароль сохранён.");
  } catch (err) {
    setText(passwordMessage, err.message);
  }
});

// ---- board sizes (workspace width / list width / card height) ----
// The values live in users.sizes (see xySizes in app.js for defaults/ranges);
// there is no board on this page, so the effect is shown on a pseudo-board:
// a to-scale wireframe of a monitor with lists of "text line" bars.

const sizesBoardW = document.getElementById("sizesBoardW");
const sizesListW = document.getElementById("sizesListW");
const sizesCardH = document.getElementById("sizesCardH");
const sizesCardFont = document.getElementById("sizesCardFont");
const preview = document.getElementById("sizesPreview");

// The pretend monitor the preview scales against: wide enough that the default
// 1512px board visibly centres, and «вся ширина» visibly fills.
const PREVIEW_SCREEN_W = 2000;
// Fake cards, as question lengths in text lines — varied so the card-height
// clamp visibly cuts some cards and not others.
const PREVIEW_CARDS = [3, 6, 1, 9, 2, 4, 7, 2];

function renderPreview() {
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

function syncSizesUI() {
  const s = sizes;
  sizesBoardW.value = String(s.boardW == null ? xySizes.BOARD_W_MAX : s.boardW);
  sizesListW.value = String(s.listW);
  sizesCardH.value = String(s.cardLines == null ? xySizes.CARD_LINES_MAX : s.cardLines);
  sizesCardFont.value = String(s.cardFont);
  document.getElementById("sizesBoardWVal").textContent = s.boardW == null ? "вся ширина" : s.boardW + " px";
  document.getElementById("sizesListWVal").textContent = s.listW + " px";
  document.getElementById("sizesCardHVal").textContent =
    s.cardLines == null ? "весь текст" : s.cardLines + (s.cardLines === 1 ? " строка" : s.cardLines < 5 ? " строки" : " строк");
  document.getElementById("sizesCardFontVal").textContent = s.cardFont + " px";
  renderPreview();
}

// Debounce the save so dragging a slider fires one request, not one per pixel.
let sizesSaveTimer = null;
function scheduleSizesSave() {
  if (sizesSaveTimer) clearTimeout(sizesSaveTimer);
  sizesSaveTimer = setTimeout(async () => {
    sizesSaveTimer = null;
    // Best-effort — the sliders already show the value, a failed save is not fatal.
    try { await jpost("/api/auth/sizes", sizes); } catch (_) {}
  }, 400);
}

function commitSizes() {
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
document.getElementById("sizesReset").addEventListener("click", () => {
  sizes = { ...xySizes.DEFAULT };
  syncSizesUI();
  scheduleSizesSave();
});

const sizesModal = wireModal("sizesOverlay", "sizesBtn", null, async () => {
  await booted;
  syncSizesUI();
});
document.getElementById("sizesClose").addEventListener("click", sizesModal.close);

// ---- default author ----
const authorForm = document.getElementById("authorForm");
const authorMessage = document.getElementById("authorMessage");
const authorModal = wireModal("authorOverlay", "authorBtn", "authorCancel", async () => {
  await booted;
  document.getElementById("authorValue").value = defaultAuthor;
  setText(authorMessage, "");
});

authorForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  setText(authorMessage, "");
  const v = document.getElementById("authorValue").value.trim();
  try {
    await jpost("/api/auth/default-author", { default_author: v });
    defaultAuthor = v;
    authorModal.close();
  } catch (err) {
    setText(authorMessage, err.message);
  }
});

document.getElementById("logoutBtn").addEventListener("click", async () => {
  try { await fetchVoid("/api/auth/logout", { method: "POST" }); } catch (_) {}
  window.location.replace("/login");
});
