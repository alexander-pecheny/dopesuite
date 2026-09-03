// Tests for passcheck.js — the monthly Passphrase Check. The clock, the store,
// the modal and the verify call are all injected, so the whole rule runs on
// plain objects: no DOM, no localStorage, no crypto.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  createPassCheck, PASSCHECK_INTERVAL_MS, PASSCHECK_PREFIX, passCheckStore,
} from "../web/assets/static/dist/passcheck.js";

const GOOD = "correct horse battery staple";
const OPEN = { owner: true, cached: true, online: true, testMode: false, deepLink: false };

function harness(opts = {}) {
  let now = opts.now ?? 10_000_000_000;
  let stamp = opts.stamp ?? null;
  const calls = { opened: [], messages: [], changePass: 0, backup: 0 };
  const node = () => ({ handlers: {}, addEventListener(type, h) { this.handlers[type] = h; } });
  const ui = {
    modal: {
      open(o = {}) { calls.opened.push(o); },
      message(t) { calls.messages.push(t); },
    },
    title: { textContent: "Помните пароль доски?" },
    step1: { hidden: true },
    step2: { hidden: true },
    form: node(),
    pass: { value: "", focused: 0, focus() { this.focused++; } },
    forgot: node(),
    backup: node(),
  };
  const check = createPassCheck({
    ui,
    now: () => now,
    read: () => stamp,
    write: (at) => { stamp = at; },
    verify: async (pass) => { if (pass !== GOOD) throw new Error("Неверный пароль доски"); },
    changePass: (onDone) => { calls.changePass++; calls.onDone = onDone; },
    backup: () => { calls.backup++; },
  });
  return {
    check, ui, calls,
    stamp: () => stamp,
    tick: (ms) => { now += ms; },
    at: () => now,
    submit: () => ui.form.handlers.submit({ preventDefault() {} }),
  };
}

// ---- when it asks ----

test("no timestamp at all: due now (every board on deploy day)", () => {
  const h = harness();
  h.check.maybe(OPEN);
  assert.equal(h.calls.opened.length, 1);
  assert.equal(h.ui.step1.hidden, false);
  assert.equal(h.ui.step2.hidden, true);
  assert.equal(h.ui.pass.focused, 1);
});

test("a stamp inside the month keeps quiet; a day past it does not", () => {
  const h = harness();
  h.check.stamp();
  h.tick(PASSCHECK_INTERVAL_MS - 1);
  h.check.maybe(OPEN);
  assert.equal(h.calls.opened.length, 0);

  h.tick(2);
  h.check.maybe(OPEN);
  assert.equal(h.calls.opened.length, 1);
});

test("asks at most once per page load", () => {
  const h = harness();
  h.check.maybe(OPEN);
  h.check.maybe(OPEN);
  assert.equal(h.calls.opened.length, 1);
});

for (const [what, when] of [
  ["a non-owner is never asked", { owner: false }],
  ["a passphrase just typed on the overlay is proof enough", { cached: false }],
  ["offline there is nothing to verify against", { online: false }],
  ["Тест-режим is not the moment", { testMode: true }],
  ["a deep link came for one question", { deepLink: true }],
]) {
  test("skipped: " + what, () => {
    const h = harness();
    h.check.maybe({ ...OPEN, ...when });
    assert.equal(h.calls.opened.length, 0);
    assert.equal(h.stamp(), null); // and the clock is untouched, so a plain open still asks
  });
}

// ---- the two ways out ----

test("the wrong passphrase keeps the modal open and says so", async () => {
  const h = harness();
  h.check.maybe(OPEN);
  h.ui.pass.value = "не тот";
  await h.submit();
  assert.deepEqual(h.calls.messages, ["", "Неверный пароль доски"]);
  assert.equal(h.ui.step1.hidden, false);
  assert.equal(h.ui.step2.hidden, true);
  assert.equal(h.stamp(), null);
});

test("the right passphrase swaps in the backup step and stamps the clock", async () => {
  const h = harness();
  h.check.maybe(OPEN);
  h.ui.pass.value = GOOD;
  await h.submit();
  assert.equal(h.ui.step1.hidden, true);
  assert.equal(h.ui.step2.hidden, false);
  assert.equal(h.ui.title.textContent, "Пароль доски"); // the heading stops asking
  assert.equal(h.stamp(), h.at());
  h.ui.backup.handlers.click();
  assert.equal(h.calls.backup, 1);
});

test("«не помню» opens the change form, whose success satisfies the check", () => {
  const h = harness();
  h.check.maybe(OPEN);
  h.ui.forgot.handlers.click();
  assert.equal(h.calls.changePass, 1);
  assert.equal(h.ui.step2.hidden, true); // not yet: the form is still open

  h.calls.onDone();
  assert.equal(h.ui.step2.hidden, false);
  assert.equal(h.stamp(), h.at());
});

// ---- not dismissable ----

test("every dismissal is vetoed until the words are known again", async () => {
  const h = harness();
  h.check.maybe(OPEN);
  const { confirm } = h.calls.opened[0];
  assert.equal(await confirm(), false);

  h.ui.pass.value = GOOD;
  await h.submit();
  assert.equal(await confirm(), true);
});

// ---- the store ----

test("the store is per board, and shrugs off a garbage slot", () => {
  const mem = new Map();
  const store = passCheckStore({
    getItem: (k) => (mem.has(k) ? mem.get(k) : null),
    setItem: (k, v) => mem.set(k, v),
  });
  assert.equal(store.read(7), null);
  store.write(7, 1234);
  assert.equal(store.read(7), 1234);
  assert.equal(store.read(8), null);
  assert.equal(mem.get(PASSCHECK_PREFIX + "7"), "1234");

  mem.set(PASSCHECK_PREFIX + "7", "вчера");
  assert.equal(store.read(7), null);
});
