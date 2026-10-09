import {test} from "node:test";
import assert from "node:assert/strict";
import EN from "../assets/dist/esm/i18nstrings_en_gen.js";
import RU from "../assets/dist/esm/i18nstrings_ru_gen.js";
import {forLang} from "../assets/dist/esm/i18nstrings.js";

// There is ONE /static/login.js for every app, so the language cannot be chosen
// when it is built. It is chosen off the page's own <html lang>, which the
// app's Chrome stamps — an app that says nothing gets the kit's default.
test("forLang picks the catalog the page's lang names", () => {
  assert.deepEqual(forLang("en"), EN);
  assert.deepEqual(forLang("en-GB"), EN);
  assert.deepEqual(forLang("EN"), EN);
  assert.deepEqual(forLang("ru"), RU);
});

test("forLang falls back to Russian for anything it has no catalog in", () => {
  assert.deepEqual(forLang(null), RU);
  assert.deepEqual(forLang(""), RU);
  assert.deepEqual(forLang("fr"), RU);
});
