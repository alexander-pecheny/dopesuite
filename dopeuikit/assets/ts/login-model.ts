// The pure kernel of the shared login flow: the telegram code lifecycle and
// the server-status → next-step decisions. login.ts renders what this module
// decides.

export type LoginStep = "method" | "code" | "username" | "link" | "password";

export interface TgStartView {
  code: string;
  botName: string;
  deepLinkLabel: string;
  deepLinkHref: string | null;
}

// /api/auth/tg/start contract: a code to forward to the bot, plus the bot's
// username when the server knows it (enables the t.me deep link).
export function tgStartView(res: { code?: string; bot_username?: string }): TgStartView {
  const code = res.code || "";
  const bot = res.bot_username || "";
  return {
    code,
    botName: bot ? "@" + bot : "",
    deepLinkLabel: bot ? "t.me/" + bot : "",
    deepLinkHref: bot ? "https://t.me/" + bot + "?start=" + encodeURIComponent(code) : null,
  };
}

// /api/auth/methods contract: which ways in this instance offers. An instance
// that runs no telegram bot says so; anything else — an older server, a failed
// request — leaves the button up, because hiding it there would be a regression.
//
// `telegram_status` says WHICH kind of no it is, so the page can name the
// problem instead of quietly dropping the button and leaving a visitor who came
// for telegram wondering where it went. A server that doesn't send one is older
// than this: fall back to the bare boolean and say nothing.
export type TelegramStatus = "ok" | "misconfigured" | "unreachable";

export interface LoginMethods {
  telegram: boolean;
  telegramNote: string;
}

const TG_NOTES: Record<string, string> = {
  misconfigured: "Телеграм-логин настроен неверно.",
  unreachable: "Бот для телеграм-логина недоступен.",
};

export function loginMethods(
  res: { telegram?: boolean; telegram_status?: string } | null | undefined,
): LoginMethods {
  const telegram = res?.telegram !== false;
  return { telegram, telegramNote: telegram ? "" : (TG_NOTES[res?.telegram_status ?? ""] ?? "") };
}

export type PollOutcome =
  | { kind: "redirect" }
  | { kind: "step"; step: "username" }
  | { kind: "message"; text: string }
  | { kind: "stale" };

export interface TgPollDeps {
  fetchStatus: (code: string) => Promise<{ status?: string }>;
  sleep: (ms: number) => Promise<void>;
}

// pollTelegram drives /api/auth/tg/status until the bot resolves the code: a
// known telegram is "ready" (log in), a new one must "choose_username". Fetch
// errors are transient (keep polling); a code restarted mid-poll goes stale
// silently so the old loop can't clobber the new code's messages.
export async function pollTelegram(code: string, isCurrent: () => boolean, deps: TgPollDeps): Promise<PollOutcome> {
  for (let i = 0; i < 120; i++) {
    await deps.sleep(1500);
    if (!isCurrent()) return { kind: "stale" };
    let st: { status?: string };
    try {
      st = await deps.fetchStatus(code);
    } catch {
      continue;
    }
    if (st.status === "ready") return { kind: "redirect" };
    if (st.status === "choose_username") return { kind: "step", step: "username" };
    if (st.status === "expired" || st.status === "not_found") {
      return { kind: "message", text: "Код истёк. Начните вход заново." };
    }
  }
  if (!isCurrent()) return { kind: "stale" };
  return { kind: "message", text: "Время ожидания вышло. Обновите страницу." };
}

export type ClaimOutcome =
  | { kind: "redirect" }
  | { kind: "step"; step: "link" }
  | { kind: "username_taken"; text: string }
  | { kind: "error"; text: string };

// /api/auth/tg/claim contract: "ready" logs in; "password_required" means the
// username is an existing password account to prove and link; "username_taken"
// bounces back to the username step.
export function claimOutcome(status: string | undefined): ClaimOutcome {
  if (status === "ready") return { kind: "redirect" };
  if (status === "password_required") return { kind: "step", step: "link" };
  if (status === "username_taken") return { kind: "username_taken", text: "Логин занят, выберите другой." };
  return { kind: "error", text: "Что-то пошло не так, попробуйте снова." };
}

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
