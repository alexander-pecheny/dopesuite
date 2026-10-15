import {test} from "node:test";
import assert from "node:assert/strict";
import {handleOf, passwordProblem, pollLink, tgLinkView, whoami} from "./dist/profile-model.js";

// The account page names a person by the first of the three things it has, and
// an account that has none of them is still somebody: the id is a name too.
test("whoami prefers the username, then the telegram, then the id", () => {
  assert.deepEqual(whoami({user_id: 7, username: "pecheny", telegram: "pecheny_tg"}), "pecheny");
  assert.deepEqual(whoami({user_id: 7, username: null, telegram: "pecheny_tg"}), "pecheny_tg");
  assert.deepEqual(whoami({user_id: 7, username: null, telegram: null}), "#7");
  assert.deepEqual(whoami({user_id: 7}), "#7");
});

test("handleOf leaves exactly one @ for the page to put back", () => {
  assert.deepEqual(handleOf("pecheny"), "pecheny");
  assert.deepEqual(handleOf("@pecheny"), "pecheny");
  assert.deepEqual(handleOf(null), "");
  assert.deepEqual(handleOf(undefined), "");
});

test("tgLinkView builds the deep link only when the bot has a name", () => {
  const view = tgLinkView({code: "ABC123", bot_username: "spliff_bot"});
  assert.deepEqual(view.code, "ABC123");
  assert.deepEqual(view.botName, "@spliff_bot");
  assert.deepEqual(view.deepLinkLabel, "t.me/spliff_bot");
  assert.deepEqual(view.deepLinkHref, "https://t.me/spliff_bot?start=ABC123");

  // A server that knows no handle would otherwise advertise a dead link.
  const nameless = tgLinkView({code: "ABC123"});
  assert.deepEqual(nameless.deepLinkHref, null);
  assert.deepEqual(nameless.botName, "");
  assert.deepEqual(nameless.code, "ABC123");
});

test("passwordProblem only judges what the page can see", () => {
  assert.deepEqual(passwordProblem("correct-horse", "correct-horse"), "");
  assert.deepEqual(passwordProblem("", ""), "");
  assert.deepEqual(passwordProblem("correct-horse", "correct horse").length > 0, true);
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

test("pollLink waits through pending and answers with the handle", async () => {
  const p = poller([{status: "pending"}, {status: "pending"}, {status: "linked", telegram: "@pecheny"}]);
  assert.deepEqual(await p.run("ABC123"), {kind: "linked", telegram: "pecheny"});
  assert.deepEqual(p.asked, ["ABC123", "ABC123", "ABC123"]);
});

test("pollLink stops on a refusal and says what the server said", async () => {
  const p = poller([{status: "pending"}, {refusal: "That Telegram account is already linked to another user."}]);
  assert.deepEqual(await p.run("ABC123"), {
    kind: "message",
    text: "That Telegram account is already linked to another user.",
  });
});

test("pollLink keeps waiting through a request that never arrived", async () => {
  const p = poller([new Error("network"), {status: "linked", telegram: "pecheny"}]);
  assert.deepEqual(await p.run("ABC123"), {kind: "linked", telegram: "pecheny"});
});

test("pollLink ends on a lapsed or forgotten code", async () => {
  for (const status of ["expired", "not_found"]) {
    const outcome = await poller([{status}]).run("ABC123");
    assert.deepEqual(outcome.kind, "message");
    assert.deepEqual(outcome.text.length > 0, true);
  }
});

test("a code restarted mid-poll leaves the old loop with nothing to say", async () => {
  const p = poller([{status: "pending"}], {current: () => false});
  assert.deepEqual(await p.run("ABC123"), {kind: "stale"});
  assert.deepEqual(p.asked, []);
});
