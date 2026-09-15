// The account page: who you are, your password, your Telegram, and the way
// out. Every decision it makes lives in profile-model.ts; this file binds them
// to the page the server compiled.

import S from "./i18nstrings.js";
import { ApiError, errorText, get, request } from "./api";
import { byId, setText, show } from "./dom";
import { type LinkStatus, type MeDTO, handleOf, passwordProblem, pollLink, tgLinkView, whoami } from "./profile-model";

const telegramLinked = byId("telegramLinked");
const telegramHandle = byId("telegramHandle");
const telegramNone = byId("telegramNone");
const telegramSection = byId("telegramSection");
const telegramHint = byId("telegramHint");
const telegramMessage = byId("telegramMessage");
const linkBtn = byId<HTMLButtonElement>("linkBtn");
const linkCodeBlock = byId("linkCodeBlock");
const linkDeepLink = byId<HTMLAnchorElement>("linkDeepLink");
const passwordForm = byId<HTMLFormElement>("passwordForm");
const passwordMessage = byId("passwordMessage");

// The code being waited on. Restarting replaces it, which is how the old poll
// learns it is stale.
let code = "";

async function load(): Promise<void> {
  try {
    showAccount(await get<MeDTO>("/api/auth/me"));
  } catch (error) {
    setText(passwordMessage, errorText(error));
  }
}

// showAccount draws the two account lines and decides whether there is anything
// to link: an account carries at most one Telegram, so once it has one the
// whole section goes away rather than offering a second.
function showAccount(me: MeDTO): void {
  setText(byId("whoami"), whoami(me));
  const handle = handleOf(me.telegram);
  if (handle) {
    linked(handle);
    show(telegramSection, false);
    return;
  }
  show(telegramLinked, false);
  show(telegramNone, true);
  show(telegramSection, true);
  void showTelegramAvailability();
}

// linked draws the account line for a Telegram that is now on the account, and
// puts the offer to link one away.
function linked(handle: string): void {
  setText(telegramHandle, "@" + handle);
  show(telegramLinked, true);
  show(telegramNone, false);
  linkBtn.hidden = true;
}

// An instance that holds no bot token advertises no way to link: the button
// would mint a code nothing will ever collect. A bot that is merely unreachable
// is a different answer and keeps its button — the code outlives the outage,
// and the person is already logged in either way.
async function showTelegramAvailability(): Promise<void> {
  try {
    const methods = await get<{ telegram_status?: string }>("/api/auth/methods");
    if (methods.telegram_status === "misconfigured") {
      linkBtn.hidden = true;
      setText(telegramHint, S.auth.tg.notConfigured());
    }
  } catch {
    // An older server, or none of our business: leave the button up rather
    // than hiding the only way in on a failed request.
  }
}

// ---- the password ----

passwordForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void setPassword();
});

async function setPassword(): Promise<void> {
  setText(passwordMessage, "");
  const next = byId<HTMLInputElement>("newPassword").value;
  const problem = passwordProblem(next, byId<HTMLInputElement>("repeatPassword").value);
  if (problem) {
    setText(passwordMessage, problem);
    return;
  }
  try {
    await request("POST", "/api/auth/password", {
      current_password: byId<HTMLInputElement>("currentPassword").value,
      new_password: next,
    });
    passwordForm.reset();
    setText(passwordMessage, S.profile.password.saved());
  } catch (error) {
    setText(passwordMessage, errorText(error));
  }
}

// ---- linking a Telegram ----

linkBtn.addEventListener("click", () => void startLink());

async function startLink(): Promise<void> {
  setText(telegramMessage, "");
  try {
    const view = tgLinkView(
      await request<{ code?: string; bot_username?: string }>("POST", "/api/auth/tg/link/start"),
    );
    code = view.code;
    setText(byId("linkCode"), view.code);
    if (view.deepLinkHref) {
      setText(byId("linkBotName"), view.botName);
      setText(linkDeepLink, view.deepLinkLabel);
      linkDeepLink.href = view.deepLinkHref;
      linkDeepLink.target = "_blank";
      linkDeepLink.rel = "noopener";
    }
    show(linkDeepLink, view.deepLinkHref !== null);
    show(linkCodeBlock, true);
    linkBtn.hidden = true;
    void poll();
  } catch (error) {
    setText(telegramMessage, errorText(error));
  }
}

async function poll(): Promise<void> {
  const waitingFor = code;
  const outcome = await pollLink(waitingFor, () => waitingFor === code, {
    fetchStatus: (c) => linkStatus(c),
    sleep,
  });
  if (outcome.kind === "stale") return;
  code = "";
  show(linkCodeBlock, false);
  if (outcome.kind === "linked") {
    // The section stays, emptied of everything but its heading and the
    // confirmation: that is where the person is looking. A reload finds an
    // account with a Telegram on it and drops the section altogether.
    show(telegramHint, false);
    linked(outcome.telegram);
    setText(telegramMessage, S.profile.telegram.linked());
    return;
  }
  setText(telegramMessage, outcome.text);
  linkBtn.hidden = false;
}

// A refusal — the Telegram is somebody else's, this account already has one —
// is the server's own sentence and ends the wait. Anything else that throws is
// a request that never arrived, and the wait goes on.
async function linkStatus(c: string): Promise<LinkStatus> {
  try {
    return await get<LinkStatus>("/api/auth/tg/link/status?code=" + encodeURIComponent(c));
  } catch (error) {
    if (error instanceof ApiError) return { refusal: error.message };
    throw error;
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// ---- the way out ----

byId("logoutBtn").addEventListener("click", () => {
  void (async (): Promise<void> => {
    try {
      await request("POST", "/api/auth/logout");
    } catch {
      // The cookie is gone either way as far as this browser is concerned.
    }
    window.location.href = "/login";
  })();
});

void load();
