import {assertEquals} from "https://deno.land/std@0.224.0/assert/mod.ts";
import EN from "../assets/dist/esm/i18nstrings_en_gen.js";
import RU from "../assets/dist/esm/i18nstrings_ru_gen.js";
import {forLang} from "../assets/dist/esm/i18nstrings.js";

// There is ONE /static/login.js for every app, so the language cannot be chosen
// when it is built. It is chosen off the page's own <html lang>, which the
// app's Chrome stamps — an app that says nothing gets the kit's default.
Deno.test("forLang picks the catalog the page's lang names", () => {
  assertEquals(forLang("en"), EN);
  assertEquals(forLang("en-GB"), EN);
  assertEquals(forLang("EN"), EN);
  assertEquals(forLang("ru"), RU);
});

Deno.test("forLang falls back to Russian for anything it has no catalog in", () => {
  assertEquals(forLang(null), RU);
  assertEquals(forLang(""), RU);
  assertEquals(forLang("fr"), RU);
});
