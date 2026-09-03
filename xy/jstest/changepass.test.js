// Tests for changepass.js — «Сменить пароль доски». Every seam is injected, so
// the PUT that would brick a board is checked on plain objects.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createChangePass } from "../web/assets/static/dist/changepass.js";

const DK = { key: "K", raw: new Uint8Array([1, 2, 3]) };

function harness(opts = {}) {
  const calls = { opened: 0, closed: 0, messages: [], puts: [], rewrapped: [], changed: 0, rolled: [], resets: 0 };
  const ui = {
    modal: {
      open() { calls.opened++; },
      close() { calls.closed++; },
      message(t) { calls.messages.push(t); },
    },
    form: { handlers: {}, addEventListener(type, h) { this.handlers[type] = h; } },
    input: { value: "" },
    setup: {
      roll: async (copy) => { calls.rolled.push(copy); },
      reset: () => { calls.resets++; },
    },
  };
  const cp = createChangePass({
    boardId: 7,
    ui,
    owner: () => opts.owner !== false,
    dk: () => DK,
    crypto: {
      validatePassphrase: (p) => (p.length < 16 ? "Слишком короткий пароль" : null),
      rewrapKey: async (p, dk) => {
        calls.rewrapped.push([p, dk]);
        if (opts.rewrapFails) throw new Error("scrypt упал");
        return { kdf_salt: "s2", kdf_params: "{}", wrapped_key: "w2" };
      },
    },
    requireOnline: (msg) => {
      if (opts.online === false) { ui.modal.message(msg); return false; }
      return true;
    },
    jput: async (url, body) => {
      calls.puts.push([url, body]);
      if (opts.putFails) throw new Error("403");
    },
    errMsg: (e) => String(e.message ?? e),
    onChanged: () => { calls.changed++; },
  });
  return { cp, ui, calls, submit: () => ui.form.handlers.submit({ preventDefault() {} }) };
}

test("opening rolls a fresh passphrase, copies it, and takes back the last promise", () => {
  const { cp, calls } = harness();
  cp.open();
  assert.equal(calls.resets, 1);
  assert.deepEqual(calls.rolled, [true]); // a menu click is a gesture, so the clipboard answers
  assert.equal(calls.opened, 1);
});

test("a weak passphrase never reaches the server", async () => {
  const { calls, ui, submit } = harness();
  ui.input.value = "коротко";
  await submit();
  assert.deepEqual(calls.messages, ["Слишком короткий пароль"]);
  assert.deepEqual(calls.puts, []);
});

test("offline it says so and writes nothing", async () => {
  const { calls, ui, submit } = harness({ online: false });
  ui.input.value = "правильная лошадь батарея скрепка";
  await submit();
  assert.deepEqual(calls.messages, ["Смена пароля доски доступна только онлайн."]);
  assert.deepEqual(calls.puts, []);
});

test("submit re-wraps the SAME key and PUTs the new keymeta", async () => {
  const { calls, ui, submit } = harness();
  ui.input.value = "правильная лошадь батарея скрепка";
  await submit();
  assert.deepEqual(calls.rewrapped, [["правильная лошадь батарея скрепка", DK]]);
  assert.deepEqual(calls.puts, [["/api/boards/7/keymeta", { kdf_salt: "s2", kdf_params: "{}", wrapped_key: "w2" }]]);
  assert.equal(calls.changed, 1); // the Passphrase Check clock is stamped
  assert.equal(calls.closed, 1);
});

test("a refused PUT keeps the form open and stamps nothing", async () => {
  const { calls, ui, submit } = harness({ putFails: true });
  ui.input.value = "правильная лошадь батарея скрепка";
  await submit();
  assert.deepEqual(calls.messages, ["403"]);
  assert.equal(calls.changed, 0);
  assert.equal(calls.closed, 0);
});

test("the callback the Passphrase Check hands in runs once, on success only", async () => {
  const h = harness();
  let done = 0;
  h.cp.open(() => { done++; });
  h.ui.input.value = "коротко";
  await h.submit();
  assert.equal(done, 0);

  h.ui.input.value = "правильная лошадь батарея скрепка";
  await h.submit();
  assert.equal(done, 1);

  await h.submit(); // the ☰ may open the form again; the check is already answered
  assert.equal(done, 1);
});

test("the ☰ offers the entry to the owner alone", () => {
  assert.equal(harness().cp.panel.offered(), true);
  assert.equal(harness({ owner: false }).cp.panel.offered(), false);
});
