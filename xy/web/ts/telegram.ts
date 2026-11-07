// telegram.ts — the page an account with no Telegram is sent to from every
// other page (requireLogin), while this instance's bot works. The bot sends a
// board's passphrase to its owner's Telegram (passphrasetg.ts), and an account
// without one would set passphrases nobody keeps a copy of. The page mints a
// code, the person sends it to the bot, and once the bot has seen it the
// Telegram is on the account and the page goes on to where they were headed.
// Until the account has put it off telegramSnoozes times (tglink.go),
// the "remind me later" button lets it go on for a day; after that, the other way out
// is logging out.
import { xyApp, type AuthMe } from "./app.js";
import S from "./i18nstrings.js";
import { qrSvg } from "./kit/qr.js";

const { fetchJSON, fetchVoid, byId, errMsg } = xyApp;

const POLL_INTERVAL_MS = 2000;
// As long as a code lives on the server: ten minutes.
const POLL_ATTEMPTS = (10 * 60 * 1000) / POLL_INTERVAL_MS;

// safeNext keeps the redirect on this site: a path, never a scheme or a
// protocol-relative //host that would make the page an open redirect.
export function safeNext(raw: string | null): string {
  if (!raw || !raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/\\")) return "/";
  return raw;
}

export type LinkOutcome =
  | { kind: "linked" }
  | { kind: "expired" }
  | { kind: "error"; text: string }
  | { kind: "stale" };

export interface LinkPollDeps {
  fetchStatus(code: string): Promise<{ status?: string }>;
  sleep(ms: number): Promise<void>;
}

// pollLink waits for the bot to see the code. A transport failure is retried
// at the next tick; an error the server words (the Telegram is someone else's)
// ends the wait, because asking again will not change it.
export async function pollLink(code: string, isCurrent: () => boolean, deps: LinkPollDeps): Promise<LinkOutcome> {
  for (let i = 0; i < POLL_ATTEMPTS; i++) {
    await deps.sleep(POLL_INTERVAL_MS);
    if (!isCurrent()) return { kind: "stale" };
    let st: { status?: string };
    try {
      st = await deps.fetchStatus(code);
    } catch (e) {
      if (e instanceof TypeError) continue;
      return { kind: "error", text: errMsg(e) };
    }
    if (st.status === "linked") return { kind: "linked" };
    if (st.status === "expired" || st.status === "not_found") return { kind: "expired" };
  }
  return isCurrent() ? { kind: "expired" } : { kind: "stale" };
}

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

let code = "";

const goOn = (): void => window.location.replace(safeNext(new URLSearchParams(location.search).get("next")));

// The day-long delay, while the account has any left.
function offerLater(left: number): void {
  if (left <= 0) return;
  byId("tgLinkLaterBlock").hidden = false;
  byId("tgLinkLaterLeft").textContent = S.chrome.telegram.laterLeft(left);
  byId("tgLinkLater").addEventListener("click", async () => {
    try {
      await fetchVoid("/api/auth/tg/link/snooze", { method: "POST" });
    } catch (e) {
      byId("tgLinkLaterMessage").textContent = errMsg(e);
      return;
    }
    code = "";
    goOn();
  });
}

async function start(): Promise<void> {
  const message = byId("tgLinkMessage");
  const retry = byId("tgLinkRetry");
  const waiting = byId("tgLinkWaiting");
  const deepLink = byId<HTMLAnchorElement>("tgLinkDeepLink");
  message.textContent = "";
  retry.hidden = true;
  let res: { code?: string; bot_username?: string };
  try {
    res = (await fetchJSON("/api/auth/tg/link/start", { method: "POST" })) as typeof res;
  } catch (e) {
    message.textContent = errMsg(e);
    retry.hidden = false;
    return;
  }
  code = res.code || "";
  const bot = res.bot_username || "";
  byId("tgLinkCode").textContent = code;
  byId("tgLinkBot").textContent = "@" + bot;
  deepLink.textContent = "t.me/" + bot;
  deepLink.href = "https://t.me/" + bot + "?start=" + encodeURIComponent(code);
  deepLink.target = "_blank";
  deepLink.rel = "noopener";
  byId("tgLinkQr").replaceChildren(qrSvg(deepLink.href, byId("tgLinkQrHint").textContent || ""));
  waiting.hidden = false;
  const forCode = code;
  const out = await pollLink(forCode, () => forCode === code, {
    fetchStatus: async (c) => (await fetchJSON("/api/auth/tg/link/status?code=" + encodeURIComponent(c))) as { status?: string },
    sleep,
  });
  if (out.kind === "stale") return;
  if (out.kind === "linked") {
    goOn();
    return;
  }
  waiting.hidden = true;
  message.textContent = out.kind === "expired" ? S.chrome.telegram.expired() : out.text;
  retry.hidden = false;
}

async function main(): Promise<void> {
  // Not through requireLogin, which would send this page to itself.
  let me: AuthMe | null = null;
  try { me = (await fetchJSON("/api/auth/me")) as AuthMe | null; } catch (_) {}
  if (!me?.user_id) {
    window.location.replace("/login");
    return;
  }
  if (!me.telegram_required) {
    goOn();
    return;
  }
  offerLater(me.telegram_snoozes_left ?? 0);
  byId("tgLinkRetry").addEventListener("click", () => void start());
  byId("tgLinkLogout").addEventListener("click", async () => {
    try { await fetchVoid("/api/auth/logout", { method: "POST" }); } catch (_) {}
    window.location.replace("/login");
  });
  void start();
}

void main();
