// tokens.js — manage API tokens for the Trello-compatible API.
// Create (shown once), list, revoke. Session-authed via /api/tokens.
import { xyApp } from "./app.js";

const { fetchJSON, jpost, jdelete, el } = xyApp;

const createForm = document.getElementById("createForm");
const createMessage = document.getElementById("createMessage");
const labelInput = document.getElementById("tokenLabel");
const newTokenSection = document.getElementById("newTokenSection");
const newTokenValue = document.getElementById("newTokenValue");
const copyTokenBtn = document.getElementById("copyTokenBtn");
const tokenList = document.getElementById("tokenList");
const emptyHint = document.getElementById("emptyHint");

function fmtDate(s) {
  if (!s) return "—";
  const d = new Date(s);
  if (isNaN(d)) return s;
  return d.toLocaleString("ru-RU", { day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" });
}

function statusText(t) {
  if (t.revoked_at) return "отозван";
  if (!t.active) return "истёк";
  return "активен";
}

async function loadTokens() {
  const tokens = await fetchJSON("/api/tokens");
  tokenList.replaceChildren();
  emptyHint.hidden = tokens.length > 0;
  for (const t of tokens) {
    const inactive = t.revoked_at || !t.active;
    const meta = `создан ${fmtDate(t.created_at)} · действует до ${fmtDate(t.expires_at)}` +
      (t.last_used_at ? ` · использован ${fmtDate(t.last_used_at)}` : " · не использовался");
    const row = el("li", { class: "token-row" + (inactive ? " token-row-inactive" : "") },
      el("div", { class: "token-row-main" },
        el("span", { class: "token-row-label", text: t.label || "(без названия)" }),
        el("span", { class: "token-row-status", text: statusText(t) }),
      ),
      el("div", { class: "token-row-meta", text: meta }),
    );
    if (!inactive) {
      row.append(el("button", {
        class: "btn btn-ghost token-revoke", type: "button",
        onclick: async () => {
          if (!confirm("Отозвать токен? Приложения, использующие его, потеряют доступ.")) return;
          try { await jdelete(`/api/tokens/${t.id}`); await loadTokens(); }
          catch (err) { alert(err.message); }
        },
      }, "Отозвать"));
    }
    tokenList.append(row);
  }
}

createForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  createMessage.textContent = "";
  try {
    const res = await jpost("/api/tokens", { label: labelInput.value.trim() });
    newTokenValue.textContent = res.token;
    newTokenSection.hidden = false;
    labelInput.value = "";
    await loadTokens();
  } catch (err) {
    createMessage.textContent = err.message;
  }
});

copyTokenBtn.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(newTokenValue.textContent);
    copyTokenBtn.textContent = "Скопировано";
    setTimeout(() => { copyTokenBtn.textContent = "Скопировать"; }, 1500);
  } catch (_) {
    // Clipboard API unavailable (e.g. non-secure context) — select for manual copy.
    const range = document.createRange();
    range.selectNodeContents(newTokenValue);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  }
});

async function boot() {
  const me = await xyApp.requireLogin();
  if (!me) return;
  try { await loadTokens(); }
  catch (err) { createMessage.textContent = err.message; }
}

boot();
