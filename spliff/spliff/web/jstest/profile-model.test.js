import {assertEquals} from "https://deno.land/std@0.224.0/assert/mod.ts";
import {handleOf, passwordProblem, pollLink, tgLinkView, whoami} from "./dist/profile-model.js";

// The account page names a person by the first of the three things it has, and
// an account that has none of them is still somebody: the id is a name too.
Deno.test("whoami prefers the username, then the telegram, then the id", () => {
  assertEquals(whoami({user_id: 7, username: "pecheny", telegram: "pecheny_tg"}), "pecheny");
  assertEquals(whoami({user_id: 7, username: null, telegram: "pecheny_tg"}), "pecheny_tg");
  assertEquals(whoami({user_id: 7, username: null, telegram: null}), "#7");
  assertEquals(whoami({user_id: 7}), "#7");
});

Deno.test("handleOf leaves exactly one @ for the page to put back", () => {
  assertEquals(handleOf("pecheny"), "pecheny");
  assertEquals(handleOf("@pecheny"), "pecheny");
  assertEquals(handleOf(null), "");
  assertEquals(handleOf(undefined), "");
});

Deno.test("tgLinkView builds the deep link only when the bot has a name", () => {
  const view = tgLinkView({code: "ABC123", bot_username: "spliff_bot"});
  assertEquals(view.code, "ABC123");
  assertEquals(view.botName, "@spliff_bot");
  assertEquals(view.deepLinkLabel, "t.me/spliff_bot");
  assertEquals(view.deepLinkHref, "https://t.me/spliff_bot?start=ABC123");

  // A server that knows no handle would otherwise advertise a dead link.
  const nameless = tgLinkView({code: "ABC123"});
  assertEquals(nameless.deepLinkHref, null);
  assertEquals(nameless.botName, "");
  assertEquals(nameless.code, "ABC123");
});

Deno.test("passwordProblem only judges what the page can see", () => {
  assertEquals(passwordProblem("correct-horse", "correct-horse"), "");
  assertEquals(passwordProblem("", ""), "");
  assertEquals(passwordProblem("correct-horse", "correct horse").length > 0, true);
});

// The poll: sleep is instant and the answers are scripted, so a wait that takes
// minutes in a browser takes no time here.
function poller(answers, {current = () => true} = {}) {
  const asked = [];
  const deps = {
    sleep: () => Promise.resolve(),
    fetchStatus: (code) => {
      asked.push(code);
      const next = answers.shift();
      if (next instanceof Error) return Promise.reject(next);
      return Promise.resolve(next);
    },
  };
  return {asked, run: (code) => pollLink(code, current, deps)};
}

Deno.test("pollLink waits through pending and answers with the handle", async () => {
  const p = poller([{status: "pending"}, {status: "pending"}, {status: "linked", telegram: "@pecheny"}]);
  assertEquals(await p.run("ABC123"), {kind: "linked", telegram: "pecheny"});
  assertEquals(p.asked, ["ABC123", "ABC123", "ABC123"]);
});

Deno.test("pollLink stops on a refusal and says what the server said", async () => {
  const p = poller([{status: "pending"}, {refusal: "That Telegram account is already linked to another user."}]);
  assertEquals(await p.run("ABC123"), {
    kind: "message",
    text: "That Telegram account is already linked to another user.",
  });
});

Deno.test("pollLink keeps waiting through a request that never arrived", async () => {
  const p = poller([new Error("network"), {status: "linked", telegram: "pecheny"}]);
  assertEquals(await p.run("ABC123"), {kind: "linked", telegram: "pecheny"});
});

Deno.test("pollLink ends on a lapsed or forgotten code", async () => {
  for (const status of ["expired", "not_found"]) {
    const outcome = await poller([{status}]).run("ABC123");
    assertEquals(outcome.kind, "message");
    assertEquals(outcome.text.length > 0, true);
  }
});

Deno.test("a code restarted mid-poll leaves the old loop with nothing to say", async () => {
  const p = poller([{status: "pending"}], {current: () => false});
  assertEquals(await p.run("ABC123"), {kind: "stale"});
  assertEquals(p.asked, []);
});
