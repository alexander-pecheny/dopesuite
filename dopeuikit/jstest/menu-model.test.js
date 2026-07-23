import { test } from "node:test";
import assert from "node:assert/strict";
import {
  accountFromMe,
  jumpFromDataset,
  menuItems,
  pickPref,
  resolveTheme,
} from "../assets/dist/esm/menu-model.js";

test("pickPref falls back on missing or unknown values", () => {
  assert.equal(pickPref(null, ["light", "dark", "system"], "system"), "system");
  assert.equal(pickPref("neon", ["light", "dark", "system"], "system"), "system");
  assert.equal(pickPref("dark", ["light", "dark", "system"], "system"), "dark");
});

test("resolveTheme honours explicit prefs and maps system to the OS scheme", () => {
  assert.equal(resolveTheme("light", true), "light");
  assert.equal(resolveTheme("dark", false), "dark");
  assert.equal(resolveTheme("system", true), "dark");
  assert.equal(resolveTheme("system", false), "light");
});

test("menuItems starts with appearance and keeps jump before extras", () => {
  const onClick = () => {};
  const items = menuItems({
    jump: { label: "Редактировать", href: "/host", external: true },
    extras: [
      { label: "Скачать", href: "/x.xlsx", download: true },
      { label: "Сбросить", onClick },
    ],
    account: null,
    config: {},
  });
  assert.deepEqual(items[0], { kind: "appearance" });
  assert.deepEqual(items[1], {
    kind: "link", label: "Редактировать", href: "/host", title: "", external: true, download: false,
  });
  assert.equal(items[2].download, true);
  assert.deepEqual(items[3], { kind: "action", label: "Сбросить", title: "", onClick });
  assert.equal(items.length, 4);
});

test("menuItems account entry uses config labels with kit defaults", () => {
  const loggedIn = menuItems({ jump: null, extras: [], account: { loggedIn: true }, config: {} });
  assert.deepEqual(loggedIn[1], {
    kind: "link", label: "Профиль", href: "/profile", title: "", external: false, download: false,
  });
  const anon = menuItems({
    jump: null,
    extras: [],
    account: { loggedIn: false },
    config: { loginHref: "/login?next=1", loginLabel: "Вход для ведущего" },
  });
  assert.equal(anon[1].href, "/login?next=1");
  assert.equal(anon[1].label, "Вход для ведущего");
});

test("jumpFromDataset reads the body data-jump-* contract", () => {
  assert.equal(jumpFromDataset({}), null);
  assert.deepEqual(jumpFromDataset({ jumpHref: "/f/1", jumpExternal: "1" }), {
    label: "Перейти", href: "/f/1", title: "", external: true,
  });
  assert.equal(jumpFromDataset({ jumpHref: "/f/2", jumpLabel: "Смотреть" }).label, "Смотреть");
});

test("accountFromMe mirrors the /api/auth/me contract", () => {
  assert.deepEqual(accountFromMe(false, null), { loggedIn: false, username: null });
  assert.deepEqual(accountFromMe(true, { username: "ap" }), { loggedIn: true, username: "ap" });
  assert.deepEqual(accountFromMe(true, { telegram: "tg" }), { loggedIn: true, username: "tg" });
  assert.deepEqual(accountFromMe(true, {}), { loggedIn: true, username: null });
});
