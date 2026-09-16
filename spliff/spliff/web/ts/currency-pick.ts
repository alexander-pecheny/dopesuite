// currency-pick.ts — which currencies the picker offers, and in what order.
//
// Pure: no DOM, no fetch. The three currency fields draw exactly what this
// says, so what the list does can be read here and tested without a browser.
//
// The rules are the ones a person at a table works by. Nothing typed: the
// currencies THIS group already deals in, then the dozen anybody is likely to
// mean, then the rest of the alphabet — the answer is usually in the first two
// rows. Something typed: a code first, because three letters typed into a
// currency field are a currency code; then a name, because somebody who knows
// the money but not its code types "lari".

import type { Choice } from "../../../../dopeuikit/assets/ts/suggest.js";

export interface Currency {
  code: string;
  name: string;
}

export interface PickList {
  /** Every currency the server offered, in code order. */
  all: Currency[];
  /**
   * What this form opens on, most relevant first: the Group's base currency,
   * then the currencies its Transactions already use. Empty on the new-group
   * form, which has no Group to have a history.
   */
  recent?: string[];
}

/**
 * The shortlist a form with no history opens on, after whatever history it
 * does have. It is not "the world's biggest currencies": it is where Spliff's
 * people live and travel, which is the only thing that makes a shortlist
 * shorter than the alphabet worth having.
 */
export const COMMON = [
  "EUR", "USD", "GBP", "GEL", "AMD", "RSD", "TRY", "RUB", "KZT", "UAH", "PLN", "CZK",
];

/** As many rows as the popover shows at once, so scrolling it is never the job. */
export const PICK_LIMIT = 12;

/** The one spelling of a code: upper case, trimmed. money.Normalise's twin. */
export function normaliseCode(typed: string): string {
  return typed.trim().toUpperCase();
}

// The pool is every currency the field may end on: what the server offered,
// plus any code this Group already uses that the rate source has since dropped.
// A Transaction keeps its own currency forever (spliff/docs/adr/0001), so the
// editor must still be able to state the one it is already in.
function poolOf(list: PickList): Currency[] {
  const out = list.all.slice();
  const known = new Set(out.map((c) => c.code));
  for (const code of list.recent ?? []) {
    if (code && !known.has(code)) {
      known.add(code);
      out.push({ code, name: "" });
    }
  }
  return out;
}

function row(currency: Currency): Choice {
  return { value: currency.code, label: currency.code, hint: currency.name, strong: true };
}

/** Does the field hold a currency this Group could actually use? */
export function isKnownCode(list: PickList, typed: string): boolean {
  const code = normaliseCode(typed);
  return poolOf(list).some((c) => c.code === code);
}

/**
 * The rows to draw for what is typed. An empty query is the opening list;
 * anything else is a code prefix first and a name substring after it.
 */
export function currencyChoices(list: PickList, query: string, limit = PICK_LIMIT): Choice[] {
  const pool = poolOf(list);
  const needle = query.trim().toLowerCase();
  if (!needle) {
    const byCode = new Map(pool.map((c) => [c.code, c]));
    const out: Choice[] = [];
    const taken = new Set<string>();
    for (const code of [...(list.recent ?? []), ...COMMON, ...pool.map((c) => c.code)]) {
      const currency = byCode.get(code);
      if (!currency || taken.has(code)) continue;
      taken.add(code);
      out.push(row(currency));
      if (out.length >= limit) break;
    }
    return out;
  }
  const hits: Choice[] = [];
  const taken = new Set<string>();
  for (const currency of pool) {
    if (!currency.code.toLowerCase().startsWith(needle)) continue;
    taken.add(currency.code);
    hits.push(row(currency));
  }
  for (const currency of pool) {
    if (taken.has(currency.code)) continue;
    if (!currency.name.toLowerCase().includes(needle)) continue;
    hits.push(row(currency));
  }
  return hits.slice(0, limit);
}
