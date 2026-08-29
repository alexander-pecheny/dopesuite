// firstrun.ts — the two questions every account has to answer once, asked on
// whichever page the user happens to open: the timezone a test session's time is
// written in, and the author name pre-filled into new question cards.
//
// Deliberately only two. The announce set and the mark template belong on
// /profile: both are meaningless before the user has run a test, and a modal
// that asks four questions on first sight is one people dismiss.
//
// A side-effect module loaded by pwa.ts, so it runs everywhere without every
// page having to opt in.

import { xyApp } from "./app.js";
import { guessZone } from "./sessions.js";
import { autocomplete, zoneChoices } from "./suggest.js";
import type { AuthMe } from "./app.js";

const { jpost, el } = xyApp;

function build(zone: string, author: string): { overlay: HTMLElement; read: () => { tz: string; author: string } } {
  const tzInput = el("input", {
    class: "input", type: "text", value: zone, placeholder: "Europe/Moscow",
    autocomplete: "off", maxlength: "64", id: "firstRunTz",
  }) as HTMLInputElement;
  const authorInput = el("input", {
    class: "input", type: "text", value: author, placeholder: "Иванов Иван",
    autocomplete: "off", maxlength: "200", id: "firstRunAuthor",
  }) as HTMLInputElement;

  const card = el("div", { class: "appearance-modal", role: "dialog", "aria-modal": "true", "aria-label": "Настройки" },
    el("h2", { class: "appearance-modal-title", text: "Пара настроек" }),
    el("p", { class: "hint", text: "Их можно поменять потом в профиле." }),
    el("label", { class: "fld-label", for: "firstRunTz", text: "Часовой пояс" }),
    tzInput,
    el("p", { class: "hint", text: "В нём записывается время тестов. Подставлен пояс этого устройства." }),
    el("label", { class: "fld-label", for: "firstRunAuthor", text: "Автор по умолчанию" }),
    authorInput,
    el("p", { class: "hint", text: "Подставляется в поле «Автор» новых вопросов." }),
  );
  // The zone box is the first thing a new account is asked for, so it gets the
  // same picker as everywhere else rather than expecting an IANA id typed blind.
  autocomplete(tzInput, zoneChoices);
  const overlay = el("div", { class: "appearance-modal-overlay", id: "firstRunOverlay" }, card);
  return {
    overlay,
    read: () => ({ tz: tzInput.value.trim(), author: authorInput.value.trim() }),
  };
}

async function firstRun(): Promise<void> {
  // Not on /join: a fresh account arriving from an invite link is asked to
  // accept it, and a modal over that button asks about a timezone for tests it
  // cannot see yet. The next page it opens will ask (ADR-0017).
  if (location.pathname.startsWith("/join/")) return;
  let me: AuthMe | null = null;
  try {
    me = (await xyApp.fetchJSON("/api/auth/me")) as AuthMe | null;
  } catch (_) {
    return; // offline or logged out: nothing to ask, and nowhere to save it
  }
  if (!me || !me.user_id || me.onboarded_at) return;

  const { overlay, read } = build(me.timezone || guessZone(), me.default_author || "");
  const card = overlay.firstElementChild as HTMLElement;

  const save = el("button", { class: "btn", type: "button", text: "Сохранить" });
  const skip = el("button", { class: "btn btn-ghost", type: "button", text: "Потом" });
  const msg = el("p", { class: "hint" });
  card.append(el("div", { class: "u-row u-gap-sm u-wrap" }, save, skip), msg);

  const finish = async (fields: Record<string, unknown>): Promise<void> => {
    try {
      await jpost("/api/auth/profile-defaults", { ...fields, onboarded: true });
      overlay.remove();
    } catch (err) {
      msg.textContent = err instanceof Error ? err.message : String(err);
    }
  };
  save.addEventListener("click", () => {
    const { tz, author } = read();
    void finish({ timezone: tz, default_author: author });
  });
  // «Потом» still stamps onboarded_at: asking again on the next page load is how
  // a modal becomes an annoyance. /profile has both fields.
  skip.addEventListener("click", () => { void finish({}); });

  document.body.append(overlay);
}

void firstRun();
