// tokens.ts — manage API tokens for the Trello-compatible API.
// Create (shown once), list, revoke. Session-authed via /api/tokens.
import { xyApp } from "./app.js";

const { fetchJSON, jpost, jdelete, el } = xyApp;

interface ApiToken {
  id: number;
  label?: string | null;
  active?: boolean;
  revoked_at?: string | null;
  created_at?: string | null;
  expires_at?: string | null;
  last_used_at?: string | null;
}

function byId<T extends HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`page is missing #${id}`);
  return node as T;
}

const errMsg = (e: unknown): string => (e instanceof Error ? e.message : String(e));

const createForm = byId<HTMLFormElement>("createForm");
const createMessage = byId("createMessage");
const labelInput = byId<HTMLInputElement>("tokenLabel");
const newTokenSection = byId("newTokenSection");
const newTokenValue = byId("newTokenValue");
const copyTokenBtn = byId("copyTokenBtn");
const tokenList = byId("tokenList");
const emptyHint = byId("emptyHint");

function fmtDate(s: string | null | undefined): string {
  if (!s) return "—";
  const d = new Date(s);
  if (isNaN(d.getTime())) return s;
  return d.toLocaleString("ru-RU", { day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" });
}

function statusText(t: ApiToken): string {
  if (t.revoked_at) return "отозван";
  if (!t.active) return "истёк";
  return "активен";
}

async function loadTokens(): Promise<void> {
  const tokens = (await fetchJSON("/api/tokens")) as ApiToken[];
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
          catch (err) { alert(errMsg(err)); }
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
    const res = (await jpost("/api/tokens", { label: labelInput.value.trim() })) as { token: string };
    newTokenValue.textContent = res.token;
    newTokenSection.hidden = false;
    labelInput.value = "";
    await loadTokens();
  } catch (err) {
    createMessage.textContent = errMsg(err);
  }
});

copyTokenBtn.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(newTokenValue.textContent ?? "");
    copyTokenBtn.textContent = "Скопировано";
    setTimeout(() => { copyTokenBtn.textContent = "Скопировать"; }, 1500);
  } catch (_) {
    // Clipboard API unavailable (e.g. non-secure context) — select for manual copy.
    const range = document.createRange();
    range.selectNodeContents(newTokenValue);
    const sel = window.getSelection();
    if (!sel) return;
    sel.removeAllRanges();
    sel.addRange(range);
  }
});

async function boot(): Promise<void> {
  const me = await xyApp.requireLogin();
  if (!me) return;
  try { await loadTokens(); }
  catch (err) { createMessage.textContent = errMsg(err); }
}

boot();
