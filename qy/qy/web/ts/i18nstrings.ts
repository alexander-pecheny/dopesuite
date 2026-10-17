// The Catalog this build renders in, the browser half of the module's
// i18nstrings package (root docs/adr/0006). qy has one language.
import RU from "./i18nstrings_ru_gen.js";
import type { Strings } from "./i18nstrings_types_gen.js";

const S: Strings = RU;
export default S;
