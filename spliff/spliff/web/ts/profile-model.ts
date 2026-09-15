// The pure kernel of the /profile page: who the account is, what a Telegram
// start answers with, and the poll that waits for the bot. No DOM, so the deno
// tests can drive the whole handshake without a browser.
//
// It is the shape of the shared login page's login-model.ts, and deliberately
// not that module: the login poll ends in a session (ready / choose_username),
// this one ends on an account that already has one (linked), and the two sets
// of statuses have nothing in common but the waiting.

import S from "./i18nstrings.js";

export interface MeDTO {
  user_id: number;
  username?: string | null;
  telegram?: string | null;
}

// whoami is the account's name as a person would say it: the username they
// chose, else the Telegram handle they came in with, else the id — which is all
// an account has before it is either.
export function whoami(me: MeDTO): string {
  return me.username || me.telegram || "#" + me.user_id;
}

// handleOf strips the @ a stored Telegram handle may or may not carry, so the
// page can put exactly one back.
export function handleOf(telegram: string | null | undefined): string {
  return (telegram ?? "").replace(/^@/, "");
}

export interface TgLinkView {
  code: string;
  botName: string;
  deepLinkLabel: string;
  deepLinkHref: string | null;
}

// The start contract, the login page's to the letter: a code to forward to the
// bot, plus the bot's username when the server knows it (which is what makes
// the t.me deep link possible).
export function tgLinkView(res: { code?: string; bot_username?: string }): TgLinkView {
  const code = res.code || "";
  const bot = res.bot_username || "";
  return {
    code,
    botName: bot ? "@" + bot : "",
    deepLinkLabel: bot ? "t.me/" + bot : "",
    deepLinkHref: bot ? "https://t.me/" + bot + "?start=" + encodeURIComponent(code) : null,
  };
}

// The two new passwords have to agree. Length is the server's to judge — it
// holds the numbers — so the page says only what it can see for itself.
export function passwordProblem(next: string, repeat: string): string {
  return next === repeat ? "" : S.profile.password.mismatch();
}

export type LinkOutcome =
  | { kind: "linked"; telegram: string }
  | { kind: "message"; text: string }
  | { kind: "stale" };

// LinkStatus is one poll's answer: the server's status, or the refusal it
// wrote instead of one. A poll that never arrived rejects, and is neither.
export interface LinkStatus {
  status?: string;
  telegram?: string | null;
  refusal?: string;
}

export interface LinkPollDeps {
  fetchStatus: (code: string) => Promise<LinkStatus>;
  sleep: (ms: number) => Promise<void>;
}

// pollLink waits for the bot to answer a link code. A refusal ends the wait
// carrying the server's own words; a request that never arrived is transient
// and the wait goes on. A code restarted mid-poll goes stale silently, so the
// old loop cannot clobber the new one's messages.
export async function pollLink(
  code: string,
  isCurrent: () => boolean,
  deps: LinkPollDeps,
): Promise<LinkOutcome> {
  for (let i = 0; i < 120; i++) {
    await deps.sleep(1500);
    if (!isCurrent()) return { kind: "stale" };
    let status: LinkStatus;
    try {
      status = await deps.fetchStatus(code);
    } catch {
      continue;
    }
    if (status.refusal) return { kind: "message", text: status.refusal };
    if (status.status === "linked") return { kind: "linked", telegram: handleOf(status.telegram) };
    if (status.status === "expired" || status.status === "not_found") {
      return { kind: "message", text: S.profile.telegram.expired() };
    }
  }
  if (!isCurrent()) return { kind: "stale" };
  return { kind: "message", text: S.profile.telegram.timedOut() };
}
