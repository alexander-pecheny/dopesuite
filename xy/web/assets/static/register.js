// register.js — invite → register code → poll status → in.
import { xyApp } from "./app.js";

const { jpost, fetchJSON } = xyApp;

const stepInvite = document.getElementById("step-invite");
const stepCode = document.getElementById("step-code");
const inviteForm = document.getElementById("inviteForm");
const inviteMessage = document.getElementById("inviteMessage");
const codeMessage = document.getElementById("codeMessage");
const regCode = document.getElementById("regCode");
const statusNode = document.getElementById("status");

function setStatus(s) { statusNode.dataset.state = s; }

inviteForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  inviteMessage.textContent = "";
  setStatus("saving");
  const code = document.getElementById("inviteCode").value.trim();
  try {
    const res = await jpost("/api/auth/register/start", { invite_code: code });
    regCode.textContent = res.code;
    stepInvite.hidden = true;
    stepCode.hidden = false;
    setStatus("saved");
    poll(res.code);
  } catch (err) {
    inviteMessage.textContent = err.message;
    setStatus("error");
  }
});

async function poll(code) {
  for (let i = 0; i < 120; i++) {
    await new Promise((r) => setTimeout(r, 1500));
    let st;
    try {
      st = await fetchJSON("/api/auth/register/status?code=" + encodeURIComponent(code));
    } catch (_) {
      continue;
    }
    if (st.status === "ready") {
      window.location.replace("/");
      return;
    }
    if (st.status === "expired" || st.status === "not_found") {
      codeMessage.textContent = "Код истёк. Начните регистрацию заново.";
      return;
    }
  }
  codeMessage.textContent = "Время ожидания вышло. Обновите страницу.";
}
