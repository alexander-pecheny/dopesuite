// The Catalog this build renders in — the browser half of the module's
// i18nstrings package (root docs/adr/0006).
//
// The kit's scripts are shared BYTE FOR BYTE by every app: there is one
// /static/login.js and one /static/menu.js for xy, dope and Spliff alike, so
// the language cannot be chosen when they are built. It is chosen here, at
// load, off the one thing the page already states about itself — the <html
// lang> its Chrome stamped (kit.Chrome.Lang). An app written in Russian says
// nothing and gets Russian; Spliff says "en" and gets English.
//
// Call sites import this, never a language's generated file.
import EN from "./i18nstrings_en_gen.js";
import RU from "./i18nstrings_ru_gen.js";
import type { Strings } from "./i18nstrings_types_gen.js";

const CATALOGS: Record<string, Strings> = { en: EN, ru: RU };

/** forLang is the Catalog a page's `lang` asks for, Russian for anything the
 * kit has no catalog in — which is what every app but Spliff is written in. */
export function forLang(lang: string | null | undefined): Strings {
  const code = (lang ?? "").trim().toLowerCase().split("-")[0];
  return CATALOGS[code] ?? RU;
}

const S: Strings = forLang(
  typeof document === "undefined" ? null : document.documentElement.getAttribute("lang"),
);
export default S;
