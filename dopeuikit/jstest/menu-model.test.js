import { test } from "node:test";
import assert from "node:assert/strict";
import {
  FONTS,
  accountFromMe,
  fontFromMe,
  fontPreload,
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

test("fontPreload names one roman face per choice, and Noto for anything else", () => {
  assert.equal(FONTS[0], "noto", "the default leads the list a picker draws");
  for (const font of FONTS) assert.match(fontPreload(font), /^\/static\/fonts\/.+\.woff2$/);
  assert.equal(new Set(FONTS.map(fontPreload)).size, FONTS.length, "one face each");
  assert.equal(fontPreload("noto"), "/static/fonts/noto-sans-var.woff2");
  assert.equal(fontPreload("literata-fix"), "/static/fonts/literata-fix.woff2");
  // A preference written by an older build must still preload something.
  assert.equal(fontPreload("comic"), "/static/fonts/noto-sans-var.woff2");
  assert.equal(pickPref("comic", FONTS, "noto"), "noto");
  assert.equal(pickPref("inter-fix-ra", FONTS, "noto"), "inter-fix-ra");
});

test("fontFromMe takes the account's face, and nothing else's word for it", () => {
  assert.equal(fontFromMe(true, { ui_font: "inter-fix-ra" }), "inter-fix-ra");
  assert.equal(fontFromMe(true, { ui_font: "noto" }), "noto");
  // No answer: nobody signed in, an app that has no such preference (dope), an
  // account that never chose, or a face this build cannot set. The browser's
  // own copy stands in all four.
  assert.equal(fontFromMe(false, null), null);
  assert.equal(fontFromMe(true, {}), null);
  assert.equal(fontFromMe(true, { ui_font: "" }), null);
  assert.equal(fontFromMe(true, { ui_font: "comic" }), null);
  assert.equal(fontFromMe(true, { ui_font: 7 }), null);
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
  assert.deepEqual(items[0], { kind: "appearance", icon: "palette" });
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
    kind: "link", label: "Профиль", href: "/profile", title: "", external: false, download: false, icon: "user",
  });
  const anon = menuItems({
    jump: null,
    extras: [],
    account: { loggedIn: false },
    config: { loginHref: "/login?next=1", loginLabel: "Вход для ведущего" },
  });
  assert.equal(anon[1].href, "/login?next=1");
  assert.equal(anon[1].label, "Вход для ведущего");
  assert.equal(anon[1].icon, "log-in");
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

test("a divider heads a cluster, and never opens the menu or doubles", () => {
  const kinds = (extras) =>
    menuItems({ jump: null, extras, account: null, config: {} }).map((i) => i.kind);

  assert.deepEqual(
    kinds([{ label: "A", onClick() {} }, { label: "B", divider: true, onClick() {} }]),
    ["appearance", "action", "divider", "action"],
  );
  assert.deepEqual(
    kinds([{ label: "A", divider: true, onClick() {} }]),
    ["appearance", "divider", "action"],
    "the appearance row is a row, so a rule after it separates something",
  );
  assert.deepEqual(
    kinds([{ label: "A", divider: true, onClick() {} }, { label: "B", divider: true, onClick() {} }]),
    ["appearance", "divider", "action", "divider", "action"],
  );
});
