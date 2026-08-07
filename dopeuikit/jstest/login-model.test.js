import {test} from "node:test";
import assert from "node:assert/strict";
import {claimOutcome, loginMethods, pollTelegram, tgStartView} from "../assets/dist/esm/login-model.js";

test("tgStartView builds the deep link only when the bot is known", () => {
  const full = tgStartView({code: "AB CD", bot_username: "dope_bot"});
  assert.equal(full.botName, "@dope_bot");
  assert.equal(full.deepLinkLabel, "t.me/dope_bot");
  assert.equal(full.deepLinkHref, "https://t.me/dope_bot?start=AB%20CD");
  assert.equal(tgStartView({code: "x"}).deepLinkHref, null);
});

function pollDeps(statuses) {
  return {
    fetchStatus: () => {
      const next = statuses.shift();
      if (next instanceof Error) return Promise.reject(next);
      return Promise.resolve({status: next});
    },
    sleep: () => Promise.resolve(),
  };
}

test("poll resolves ready → redirect, surviving transient fetch errors", async () => {
  const outcome = await pollTelegram("c", () => true, pollDeps([new Error("net"), "pending", "ready"]));
  assert.deepEqual(outcome, {kind: "redirect"});
});

test("poll routes a fresh telegram to the username step", async () => {
  const outcome = await pollTelegram("c", () => true, pollDeps(["choose_username"]));
  assert.deepEqual(outcome, {kind: "step", step: "username"});
});

test("poll reports an expired code", async () => {
  const outcome = await pollTelegram("c", () => true, pollDeps(["expired"]));
  assert.equal(outcome.kind, "message");
  assert.match(outcome.text, /истёк/);
});

test("a restarted code goes stale without a message", async () => {
  const outcome = await pollTelegram("old", () => false, pollDeps(["ready"]));
  assert.deepEqual(outcome, {kind: "stale"});
});

test("poll times out after 120 attempts with a message", async () => {
  const statuses = Array.from({length: 200}, () => "pending");
  const outcome = await pollTelegram("c", () => true, pollDeps(statuses));
  assert.equal(outcome.kind, "message");
  assert.match(outcome.text, /Время ожидания/);
  assert.equal(statuses.length, 200 - 120);
});

test("claim outcomes map every server status", () => {
  assert.deepEqual(claimOutcome("ready"), {kind: "redirect"});
  assert.deepEqual(claimOutcome("password_required"), {kind: "step", step: "link"});
  assert.equal(claimOutcome("username_taken").kind, "username_taken");
  assert.equal(claimOutcome(undefined).kind, "error");
  assert.equal(claimOutcome("garbage").kind, "error");
});

test("telegram login is offered unless the server denies it", () => {
  assert.equal(loginMethods({telegram: false}).telegram, false);
  assert.equal(loginMethods({telegram: true}).telegram, true);
  assert.equal(loginMethods({}).telegram, true);
  assert.equal(loginMethods(null).telegram, true);
});

// A visitor who came for telegram should learn the instance is broken, not that
// it quietly changed — so a "no" that names its reason carries a note.
test("a refused telegram login says which kind of no it is", () => {
  const bad = loginMethods({telegram: false, telegram_status: "misconfigured"});
  assert.equal(bad.telegram, false);
  assert.match(bad.telegramNote, /настроен неверно/);

  const down = loginMethods({telegram: false, telegram_status: "unreachable"});
  assert.match(down.telegramNote, /недоступен/);

  // A server older than telegram_status says nothing rather than guessing.
  assert.equal(loginMethods({telegram: false}).telegramNote, "");
  // And a working one has nothing to say.
  assert.equal(loginMethods({telegram: true, telegram_status: "ok"}).telegramNote, "");
});
