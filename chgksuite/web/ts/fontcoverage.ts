// What the PDF export can show. A browser always finds some font for a
// character, so an author sees everything they type; typst sees only the faces
// chgksuite embeds (Noto Sans and chgksuite/fallbackfont), and a character none
// of them has comes out of the PDF as a box. fontcoverage_gen.ts is those
// faces' characters, written by scripts/fallbackfonts.py.

import { COVERED } from "./fontcoverage_gen.js";

// Characters that draw nothing anyway — joiners, variation selectors, marks of
// direction, controls, spaces — are never reported.
const INVISIBLE = /[\p{Default_Ignorable_Code_Point}\p{Cc}\p{Zs}\p{Zl}\p{Zp}]/u;

// A range in COVERED is two numbers, its first and last code point.
const PAIR = 2;
const HEX = 16;
// U+ notation pads a code point to at least four hex digits.
const U_DIGITS = 4;

// printable reports whether the PDF export has a face for the character.
export function printable(ch: string): boolean {
  if (INVISIBLE.test(ch)) return true;
  const cp = ch.codePointAt(0) ?? 0;
  let lo = 0;
  let hi = COVERED.length / PAIR - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (cp < COVERED[PAIR * mid]) hi = mid - 1;
    else if (cp > COVERED[PAIR * mid + 1]) lo = mid + 1;
    else return true;
  }
  return false;
}

// unprintable lists, once each and in order of appearance, the characters of
// text the PDF export would show as a box.
export function unprintable(text: string): string[] {
  const out = new Set<string>();
  for (const ch of text) if (!printable(ch)) out.add(ch);
  return [...out];
}

// describe names characters for a person: each one and its code point, since
// the character itself may show as a box on their screen too.
export function describe(chars: string[]): string {
  return chars.map((ch) => `${ch} (U+${(ch.codePointAt(0) ?? 0).toString(HEX).toUpperCase().padStart(U_DIGITS, "0")})`).join(", ");
}
