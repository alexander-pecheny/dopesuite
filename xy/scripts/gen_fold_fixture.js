// Regenerates internal/xycli/testdata/fold.json — the corpus that pins xy-cli's
// Folding against the browser's (find.ts foldSearch), so a search from the shell
// finds what a search in the app finds.
//   deno run --allow-read --allow-write scripts/gen_fold_fixture.js
import { xyFind } from "../web/assets/static/dist/find.js";

const cases = [
  "Гоголь", "ГО́ГОЛЬ", "Ёлка и ёж", "Ёж", "«Ревизор»", "„Ревизор“", "“Ревизор”", "‘Ревизор’",
  "тире — и дефис ‑ и минус − и ‐ и ‒ и –",
  "нераз рывный", "не­раз", "Mixed CASE Latin", "цифры 1917 г.",
  "ударе́ние в середине", "  пробелы  по краям  ",
  "emoji 🎲 остаётся", "", "Ё и ё вместе", "«ёлочки» и „лапки“",
];

const out = cases.map((text) => ({ text, folded: xyFind.foldSearch(text) }));
const path = new URL("../internal/xycli/testdata/fold.json", import.meta.url);
await Deno.writeTextFile(path, JSON.stringify(out, null, 1) + "\n");
console.log("wrote", path.pathname);
