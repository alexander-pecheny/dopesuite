// Shared login for the dopesuite apps. One /login page, two ways in:
//   • «Войти через телеграм» — mint a code, forward it to the bot, poll. A known
//     telegram logs straight in; a brand-new one picks a username (and, if that
//     username is an existing password account, proves the password to link it).
//   • «Войти по паролю» — username + password, existing accounts only.
// Registration is not a separate flow: the telegram button creates the account.

const statusNode = document.getElementById("status");

const steps = {
  method: document.getElementById("step-method"),
  code: document.getElementById("step-code"),
  username: document.getElementById("step-username"),
  link: document.getElementById("step-link"),
  password: document.getElementById("step-password"),
};

const tgLoginBtn = document.getElementById("tgLoginBtn");
const pwLoginBtn = document.getElementById("pwLoginBtn");
const methodMessage = document.getElementById("methodMessage");

const tgDeepLink = document.getElementById("tgDeepLink");
const tgBotName = document.getElementById("tgBotName");
const tgCode = document.getElementById("tgCode");
const codeMessage = document.getElementById("codeMessage");

const usernameForm = document.getElementById("usernameForm");
const tgUsername = document.getElementById("tgUsername");
const usernameMessage = document.getElementById("usernameMessage");

const linkForm = document.getElementById("linkForm");
const linkPassword = document.getElementById("linkPassword");
const linkMessage = document.getElementById("linkMessage");
const linkCancelBtn = document.getElementById("linkCancelBtn");

const passwordForm = document.getElementById("passwordForm");
const pwUsername = document.getElementById("pwUsername");
const pwPassword = document.getElementById("pwPassword");
const passwordMessage = document.getElementById("passwordMessage");

let code = "";
let username = "";
let polling = false;

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
  showStep("method");
}

tgLoginBtn.addEventListener("click", startTelegram);
pwLoginBtn.addEventListener("click", () => {
  setText(passwordMessage, "");
  showStep("password");
});

makeCopyable(tgBotName);
makeCopyable(tgCode);

async function startTelegram() {
  setText(methodMessage, "");
  setStatus("saving");
  try {
    const res = await fetchJSON("/api/auth/tg/start", {method: "POST"});
    code = res.code;
    showCode(res.bot_username || "");
    setText(codeMessage, "");
    showStep("code");
    setStatus("saved");
    poll();
  } catch (error) {
    setText(methodMessage, error.message);
    setStatus("error");
  }
}

function showCode(bot) {
  tgCode.textContent = code;
  if (bot) {
    tgBotName.textContent = "@" + bot;
    tgDeepLink.textContent = "t.me/" + bot;
    tgDeepLink.href = "https://t.me/" + bot + "?start=" + encodeURIComponent(code);
    tgDeepLink.target = "_blank";
    tgDeepLink.rel = "noopener";
    tgDeepLink.hidden = false;
  } else {
    tgDeepLink.hidden = true;
  }
}

// makeCopyable turns an inline token into a click-to-copy control that flashes a
// «скопировано» confirmation (the .copied CSS tooltip).
function makeCopyable(el) {
  if (!el) return;
  el.classList.add("copyable");
  el.setAttribute("role", "button");
  el.setAttribute("tabindex", "0");
  const copy = async () => {
    const text = el.textContent.replace(/^@/, "");
    try {
      await navigator.clipboard.writeText(text);
    } catch (_) {
      return;
    }
    el.classList.add("copied");
    setTimeout(() => el.classList.remove("copied"), 1000);
  };
  el.addEventListener("click", copy);
  el.addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      copy();
    }
  });
}

async function poll() {
  if (polling) return;
  polling = true;
  const forCode = code;
  for (let i = 0; i < 120 && forCode === code; i++) {
    await sleep(1500);
    let st;
    try {
      st = await fetchJSON("/api/auth/tg/status?code=" + encodeURIComponent(forCode));
    } catch (_) {
      continue;
    }
    if (st.status === "ready") {
      redirectToHost();
      polling = false;
      return;
    }
    if (st.status === "choose_username") {
      showStep("username");
      polling = false;
      return;
    }
    if (st.status === "expired" || st.status === "not_found") {
      setText(codeMessage, "Код истёк. Начните вход заново.");
      polling = false;
      return;
    }
  }
  polling = false;
  setText(codeMessage, "Время ожидания вышло. Обновите страницу.");
}

usernameForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  username = tgUsername.value.trim();
  await claim("", usernameMessage);
});

linkForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  await claim(linkPassword.value, linkMessage);
});

linkCancelBtn.addEventListener("click", () => {
  linkPassword.value = "";
  setText(linkMessage, "");
  setText(usernameMessage, "");
  showStep("username");
});

async function claim(password, messageNode) {
  setText(messageNode, "");
  setStatus("saving");
  try {
    const res = await fetchJSON("/api/auth/tg/claim", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({code, username, password}),
    });
    if (res.status === "ready") {
      redirectToHost();
      return;
    }
    if (res.status === "password_required") {
      linkPassword.value = "";
      showStep("link");
      setStatus("saved");
      return;
    }
    if (res.status === "username_taken") {
      setText(usernameMessage, "Логин занят, выберите другой.");
      showStep("username");
      setStatus("error");
      return;
    }
    setText(messageNode, "Что-то пошло не так, попробуйте снова.");
    setStatus("error");
  } catch (error) {
    setText(messageNode, error.message);
    setStatus("error");
  }
}

passwordForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setStatus("saving");
  setText(passwordMessage, "");
  try {
    await fetchJSON("/api/auth/login-password", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({username: pwUsername.value.trim(), password: pwPassword.value}),
    });
    setStatus("saved");
    redirectToHost();
  } catch (error) {
    setText(passwordMessage, error.message);
    setStatus("error");
  }
});

function showStep(name) {
  for (const [key, node] of Object.entries(steps)) {
    if (node) node.hidden = key !== name;
  }
  const input = steps[name] && steps[name].querySelector("input");
  if (input) requestAnimationFrame(() => input.focus());
}

function redirectToHost() {
  const marked = document.querySelector("[data-login-redirect]");
  window.location.replace(marked?.getAttribute("data-login-redirect") || "/");
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
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

function setText(node, text) {
  if (node) node.textContent = text;
}

function setStatus(state) {
  if (!statusNode) return;
  const labels = {saved: "Готово", saving: "Подождите", error: "Ошибка"};
  statusNode.dataset.state = state;
  statusNode.setAttribute("aria-label", labels[state] || labels.saved);
  statusNode.title = labels[state] || labels.saved;
}
