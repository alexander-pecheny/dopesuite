const statusNode = document.getElementById("status");
const stepForm = document.getElementById("step-form");
const stepDone = document.getElementById("step-done");
const form = document.getElementById("loginForm");
const codeInput = document.getElementById("loginCode");
const message = document.getElementById("loginMessage");
const botLink = document.getElementById("botLink");
const passwordForm = document.getElementById("passwordForm");
const passwordUsername = document.getElementById("passwordUsername");
const passwordValue = document.getElementById("passwordValue");
const passwordMessage = document.getElementById("passwordMessage");

const botName = "dope_pecheny_bot";
botLink.href = `https://t.me/${botName}`;
botLink.textContent = `@${botName}`;

bootstrap();

async function bootstrap() {
  try {
    const me = await fetchJSON("/api/auth/me");
    if (me && me.user_id) {
      showStep(stepDone);
      return;
    }
  } catch (_) {
    // not logged in — fine
  }
  showStep(stepForm);
}

form.addEventListener("submit", async (event) => {
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
    showStep(stepDone);
  } catch (error) {
    setText(message, error.message);
    setStatus("error");
  }
});

passwordForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setStatus("saving");
  setText(passwordMessage, "");
  try {
    const username = passwordUsername.value.trim();
    const password = passwordValue.value;
    await fetchJSON("/api/auth/login-password", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({username, password}),
    });
    setStatus("saved");
    showStep(stepDone);
  } catch (error) {
    setText(passwordMessage, error.message);
    setStatus("error");
  }
});

function showStep(target) {
  for (const step of [stepForm, stepDone]) {
    step.hidden = step !== target;
  }
}

async function fetchJSON(url, init) {
  const response = await fetch(url, init);
  if (!response.ok) {
    const text = await response.text();
    throw new Error(text || `HTTP ${response.status}`);
  }
  if (response.status === 204) return null;
  return response.json();
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
