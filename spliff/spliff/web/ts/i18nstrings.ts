// The Catalog this build renders in — the browser half of the module's
// i18nstrings package (root docs/adr/0006). Call sites import this, never a
// language's generated file. Spliff has one language, and this is where that
// fact lives on the browser side.
import EN from "./i18nstrings_en_gen.js";
import type { Strings } from "./i18nstrings_types_gen.js";

const S: Strings = EN;
export default S;
