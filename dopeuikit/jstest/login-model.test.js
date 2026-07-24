import {test} from "node:test";
import assert from "node:assert/strict";
import {claimOutcome, pollTelegram, tgStartView} from "../assets/dist/esm/login-model.js";

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
