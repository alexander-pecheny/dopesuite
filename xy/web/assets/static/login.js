const statusNode = document.getElementById("status");
const stepUsername = document.getElementById("step-username");
const stepPassword = document.getElementById("step-password");
const stepCode = document.getElementById("step-code");

const usernameForm = document.getElementById("usernameForm");
const loginUsername = document.getElementById("loginUsername");
const usernameMessage = document.getElementById("usernameMessage");

const passwordLogin = document.getElementById("passwordLogin");
const passwordForm = document.getElementById("passwordForm");
const passwordValue = document.getElementById("passwordValue");
const passwordMessage = document.getElementById("passwordMessage");
const requestCodeButton = document.getElementById("requestCodeButton");

const codeLogin = document.getElementById("codeLogin");
const codeForm = document.getElementById("codeForm");
const codeInput = document.getElementById("loginCode");
const message = document.getElementById("loginMessage");

let currentUsername = "";

bootstrap();

async function bootstrap() {
  try {
    const me = await fetchJSON("/api/auth/me");
    if (me && me.user_id) {
      redirectToHost();
      return;
    }
  } catch (_) {
    // not logged in — fine
  }
  showStep(stepUsername);
}

usernameForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const username = loginUsername.value.trim();
  setStatus("saving");
  clearMessages();
  try {
    const result = await startLogin(username, false);
    currentUsername = result.username || username;
    if (result.has_password) {
      showPasswordStep();
    } else {
      showCodeStep();
    }
    setStatus("saved");
  } catch (error) {
    setText(usernameMessage, error.message);
    setStatus("error");
  }
});

passwordForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setStatus("saving");
  setText(passwordMessage, "");
  try {
    const password = passwordValue.value;
    await fetchJSON("/api/auth/login-password", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({username: currentUsername, password}),
    });
    setStatus("saved");
    redirectToHost();
  } catch (error) {
    setText(passwordMessage, error.message);
    setStatus("error");
  }
});

requestCodeButton.addEventListener("click", async () => {
  setStatus("saving");
  setText(passwordMessage, "");
  try {
    const result = await startLogin(currentUsername, true);
    currentUsername = result.username || currentUsername;
    showCodeStep();
    setStatus("saved");
  } catch (error) {
    setText(passwordMessage, error.message);
    setStatus("error");
  }
});

codeForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setStatus("saving");
  setText(message, "");
  try {
    const code = codeInput.value.trim().toUpperCase();
    await fetchJSON("/api/auth/login", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({code}),
    });
    setStatus("saved");
    redirectToHost();
  } catch (error) {
    setText(message, error.message);
    setStatus("error");
  }
});

async function startLogin(username, sendCode) {
  return fetchJSON("/api/auth/login/start", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({username, send_code: sendCode}),
  });
}

function showPasswordStep() {
  passwordLogin.textContent = currentUsername;
  passwordValue.value = "";
  showStep(stepPassword);
}

function showCodeStep() {
  codeLogin.textContent = currentUsername;
  codeInput.value = "";
  setText(message, "Введите код из Telegram.");
  showStep(stepCode);
}

function showStep(target) {
  for (const step of [stepUsername, stepPassword, stepCode]) {
    step.hidden = step !== target;
  }
  const input = target.querySelector("input");
  if (input) {
    requestAnimationFrame(() => input.focus());
  }
}

function redirectToHost() {
  window.location.replace("/");
}

async function fetchJSON(url, init) {
  const response = await fetch(url, init);
  if (!response.ok) {
    const text = (await response.text()).trim();
    throw new Error(text || `HTTP ${response.status}`);
  }
  if (response.status === 204) return null;
  return response.json();
}

function clearMessages() {
  for (const node of [usernameMessage, passwordMessage, message]) {
    setText(node, "");
  }
}

function setText(node, text) {
  node.textContent = text;
}

function setStatus(state) {
  const labels = {saved: "Готово", saving: "Подождите", error: "Ошибка"};
  statusNode.dataset.state = state;
  statusNode.setAttribute("aria-label", labels[state] || labels.saved);
  statusNode.title = labels[state] || labels.saved;
}
