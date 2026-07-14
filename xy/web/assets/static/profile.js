// profile.js — username + password management, logout.
import { xyApp } from "./app.js";

const { fetchJSON, jpost, fetchVoid } = xyApp;

const whoami = document.getElementById("whoami");
const usernameSection = document.getElementById("usernameSection");
const usernameForm = document.getElementById("usernameForm");
const usernameMessage = document.getElementById("usernameMessage");
const passwordForm = document.getElementById("passwordForm");
const passwordMessage = document.getElementById("passwordMessage");

function setText(node, t) { node.textContent = t; }

async function boot() {
  const me = await xyApp.requireLogin();
  if (!me) return;
  whoami.textContent = me.username || me.telegram || ("#" + me.user_id);
  if (!me.username) usernameSection.hidden = false;
}

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

document.getElementById("logoutBtn").addEventListener("click", async () => {
  try { await fetchVoid("/api/auth/logout", { method: "POST" }); } catch (_) {}
  window.location.replace("/login");
});

boot();
