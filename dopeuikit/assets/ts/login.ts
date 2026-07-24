// Shared login for the dopesuite apps. One /login page, two ways in:
//   • «Войти через телеграм» — mint a code, forward it to the bot, poll. A known
//     telegram logs straight in; a brand-new one picks a username (and, if that
//     username is an existing password account, proves the password to link it).
//   • «Войти по паролю» — username + password, existing accounts only.
// Registration is not a separate flow: the telegram button creates the account.
// The flow decisions live in login-model.ts; this file only binds the DOM.

import { claimOutcome, errorMessage, pollTelegram, tgStartView } from "./login-model";

function byId<T extends HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`login page is missing #${id}`);
  return node as T;
}

const statusNode = document.getElementById("status");

const steps: Record<string, HTMLElement> = {
  method: byId("step-method"),
  code: byId("step-code"),
  username: byId("step-username"),
  link: byId("step-link"),
  password: byId("step-password"),
};

const tgLoginBtn = byId<HTMLButtonElement>("tgLoginBtn");
const pwLoginBtn = byId<HTMLButtonElement>("pwLoginBtn");
const methodMessage = byId("methodMessage");

const tgDeepLink = byId<HTMLAnchorElement>("tgDeepLink");
const tgBotName = byId("tgBotName");
const tgCode = byId("tgCode");
const codeMessage = byId("codeMessage");

const usernameForm = byId<HTMLFormElement>("usernameForm");
const tgUsername = byId<HTMLInputElement>("tgUsername");
const usernameMessage = byId("usernameMessage");

const linkForm = byId<HTMLFormElement>("linkForm");
const linkPassword = byId<HTMLInputElement>("linkPassword");
const linkMessage = byId("linkMessage");
const linkCancelBtn = byId<HTMLButtonElement>("linkCancelBtn");

const passwordForm = byId<HTMLFormElement>("passwordForm");
const pwUsername = byId<HTMLInputElement>("pwUsername");
const pwPassword = byId<HTMLInputElement>("pwPassword");
const passwordMessage = byId("passwordMessage");

let code = "";
let username = "";
let polling = false;

void bootstrap();

async function bootstrap(): Promise<void> {
  try {
    const me = await fetchJSON("/api/auth/me");
    if (me && (me as { user_id?: number }).user_id) {
      redirectToHost();
      return;
    }
  } catch {
    // not logged in — fine
  }
  showStep("method");
}

tgLoginBtn.addEventListener("click", () => void startTelegram());
pwLoginBtn.addEventListener("click", () => {
  setText(passwordMessage, "");
  showStep("password");
});

makeCopyable(tgBotName);
makeCopyable(tgCode);

async function startTelegram(): Promise<void> {
  setText(methodMessage, "");
  setStatus("saving");
  try {
    const res = tgStartView((await fetchJSON("/api/auth/tg/start", { method: "POST" })) as {
      code?: string;
      bot_username?: string;
    });
    code = res.code;
    showCode(res);
    setText(codeMessage, "");
    showStep("code");
    setStatus("saved");
    void poll();
  } catch (error) {
    setText(methodMessage, errorMessage(error));
    setStatus("error");
  }
}

function showCode(view: ReturnType<typeof tgStartView>): void {
  tgCode.textContent = view.code;
  if (view.deepLinkHref) {
    tgBotName.textContent = view.botName;
    tgDeepLink.textContent = view.deepLinkLabel;
    tgDeepLink.href = view.deepLinkHref;
    tgDeepLink.target = "_blank";
    tgDeepLink.rel = "noopener";
    tgDeepLink.hidden = false;
  } else {
    tgDeepLink.hidden = true;
  }
}

// makeCopyable turns an inline token into a click-to-copy control that flashes a
// «скопировано» confirmation (the .copied CSS tooltip).
function makeCopyable(el: HTMLElement): void {
  el.classList.add("copyable");
  el.setAttribute("role", "button");
  el.setAttribute("tabindex", "0");
  const copy = async (): Promise<void> => {
    const text = (el.textContent ?? "").replace(/^@/, "");
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      return;
    }
    el.classList.add("copied");
    setTimeout(() => el.classList.remove("copied"), 1000);
  };
  el.addEventListener("click", () => void copy());
  el.addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      void copy();
    }
  });
}

async function poll(): Promise<void> {
  if (polling) return;
  polling = true;
  const forCode = code;
  const outcome = await pollTelegram(forCode, () => forCode === code, {
    fetchStatus: async (c) =>
      (await fetchJSON("/api/auth/tg/status?code=" + encodeURIComponent(c))) as { status?: string },
    sleep,
  });
  polling = false;
  if (outcome.kind === "redirect") redirectToHost();
  else if (outcome.kind === "step") showStep(outcome.step);
  else if (outcome.kind === "message") setText(codeMessage, outcome.text);
}

usernameForm.addEventListener("submit", (event) => {
  event.preventDefault();
  username = tgUsername.value.trim();
  void claim("", usernameMessage);
});

linkForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void claim(linkPassword.value, linkMessage);
});

linkCancelBtn.addEventListener("click", () => {
  linkPassword.value = "";
  setText(linkMessage, "");
  setText(usernameMessage, "");
  showStep("username");
});

async function claim(password: string, messageNode: HTMLElement): Promise<void> {
  setText(messageNode, "");
  setStatus("saving");
  try {
    const res = (await fetchJSON("/api/auth/tg/claim", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code, username, password }),
    })) as { status?: string };
    const outcome = claimOutcome(res.status);
    if (outcome.kind === "redirect") {
      redirectToHost();
      return;
    }
    if (outcome.kind === "step") {
      linkPassword.value = "";
      showStep(outcome.step);
      setStatus("saved");
      return;
    }
    if (outcome.kind === "username_taken") {
      setText(usernameMessage, outcome.text);
      showStep("username");
      setStatus("error");
      return;
    }
    setText(messageNode, outcome.text);
    setStatus("error");
  } catch (error) {
    setText(messageNode, errorMessage(error));
    setStatus("error");
  }
}

passwordForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void (async (): Promise<void> => {
    setStatus("saving");
    setText(passwordMessage, "");
    try {
      await fetchJSON("/api/auth/login-password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: pwUsername.value.trim(), password: pwPassword.value }),
      });
      setStatus("saved");
      redirectToHost();
    } catch (error) {
      setText(passwordMessage, errorMessage(error));
      setStatus("error");
    }
  })();
});

function showStep(name: string): void {
  for (const [key, node] of Object.entries(steps)) {
    node.hidden = key !== name;
  }
  const input = steps[name]?.querySelector("input");
  if (input) requestAnimationFrame(() => input.focus());
}

function redirectToHost(): void {
  const marked = document.querySelector("[data-login-redirect]");
  window.location.replace(marked?.getAttribute("data-login-redirect") || "/");
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function fetchJSON(url: string, init?: RequestInit): Promise<unknown> {
  const response = await fetch(url, init);
  if (!response.ok) {
    const text = (await response.text()).trim();
    throw new Error(text || `HTTP ${response.status}`);
  }
  if (response.status === 204) return null;
  return response.json();
}

function setText(node: HTMLElement, text: string): void {
  node.textContent = text;
}

function setStatus(state: "saved" | "saving" | "error"): void {
  if (!statusNode) return;
  const labels = { saved: "Готово", saving: "Подождите", error: "Ошибка" };
  statusNode.dataset.state = state;
  statusNode.setAttribute("aria-label", labels[state]);
  statusNode.title = labels[state];
}
