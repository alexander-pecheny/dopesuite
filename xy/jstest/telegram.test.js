// The /telegram page: where it may send you on, and how the wait for the bot ends.
import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

// The module runs main() on import; a logged-out answer sends it to /login and ends it there.
installDOM(["tgLinkMessage", "tgLinkRetry", "tgLinkWaiting", "tgLinkDeepLink", "tgLinkBot", "tgLinkCode", "tgLinkLogout"]);
location.replace = () => {};
globalThis.fetch = async () => ({ ok: false, status: 401, text: async () => "" });
const { safeNext, pollLink } = await import("../web/assets/static/dist/telegram.js");

test("next stays on this site", () => {
  assert.equal(safeNext("/board/7?card=3"), "/board/7?card=3");
  for (const bad of [null, "", "https://evil.example", "//evil.example", "/\\evil.example", "javascript:alert(1)"]) {
    assert.equal(safeNext(bad), "/", String(bad));
  }
});

const sleep = async () => {};

test("the wait ends once the bot has linked the account", async () => {
  const answers = [{ status: "pending" }, { status: "linked" }];
  const out = await pollLink("C", () => true, { sleep, fetchStatus: async () => answers.shift() });
  assert.deepEqual(out, { kind: "linked" });
});

test("a network blip is retried; a refusal the server words ends the wait", async () => {
  let n = 0;
  const out = await pollLink("C", () => true, {
    sleep,
    fetchStatus: async () => {
      n++;
      if (n === 1) throw new TypeError("offline");
      throw new Error("этот телеграм уже привязан к другому аккаунту");
    },
  });
  assert.equal(out.kind, "error");
  assert.match(out.text, /другому аккаунту/);
});

test("an expired code says so, and a replaced one goes quiet", async () => {
  assert.deepEqual(await pollLink("C", () => true, { sleep, fetchStatus: async () => ({ status: "expired" }) }), { kind: "expired" });
  assert.deepEqual(await pollLink("C", () => false, { sleep, fetchStatus: async () => ({ status: "linked" }) }), { kind: "stale" });
});
