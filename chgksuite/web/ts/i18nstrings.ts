// The Catalog the question renderer shows — the browser half of chgksuite's
// i18nstrings package (root docs/adr/0006). Call sites import this, never a
// language's generated file, so choosing another one is an edit here.
import RU from "./i18nstrings_ru_gen.js";
import type { Strings } from "./i18nstrings_types_gen.js";

const S: Strings = RU;
export default S;
