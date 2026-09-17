// currency-field.ts — every currency field on a page, bound the same way once.
//
// The field used to be a native <select> of every code the rate source
// publishes. On a phone that picker cannot be typed into: entering GEL meant
// spinning a wheel past 160 rows, which the owner called unusable, and on a
// desktop the same field types fine, which is why it shipped. It is the kit's
// suggest now — a text field that filters as you type.
//
// What the list draws is currency-pick.ts, which has no DOM in it. This module
// is the adapter: the one fetch of /api/currencies, the binding, and the
// refusal a code nobody quotes earns before the form is sent rather than after.

import { autocomplete } from "../../../../dopeuikit/assets/ts/suggest.js";
import { get, type CurrencyDTO } from "./api";
import { currencyChoices, isKnownCode, normaliseCode, type PickList } from "./currency-pick";
import { setText, show } from "./dom";
import S from "./i18nstrings.js";

// One fetch per page: the table is the same for every field on it, and a Group
// with four currency fields would otherwise ask four times.
let table: Promise<CurrencyDTO[]> | null = null;

function currencies(): Promise<CurrencyDTO[]> {
  if (!table) table = get<CurrencyDTO[]>("/api/currencies").catch(() => []);
  return table;
}

// And one list per page, for the same reason the fetch is: the history that
// puts GEL at the top of the list is the Group's, not one field's. The
// Transaction editor draws a currency cell in every row of its two tables, and
// they all open on the same rows in the same order.
let pool: PickList = { all: [] };

export interface CurrencyField {
  /**
   * Fill the field with `code` and say what this form's history is — the
   * Group's base currency first, then the currencies its Transactions use.
   * The new-group form passes none: it has no Group to have a history.
   */
  fill(code: string, recent?: string[]): Promise<void>;
  /** The code the form submits. */
  value(): string;
  /**
   * Is what is typed a currency? A refusal is drawn under the field and the
   * caller does not send the form.
   */
  ok(): boolean;
}

export function bindCurrency(input: HTMLInputElement, error: HTMLElement): CurrencyField {
  const clear = (): void => show(error, false);

  suggest(input);
  input.addEventListener("input", clear);

  return {
    async fill(code, recent) {
      pool = { all: await currencies(), recent: (recent ?? []).filter(Boolean) };
      input.value = normaliseCode(code);
      clear();
    },
    // A half-typed "ge" is not a currency yet: the readouts that ask keep the
    // last code they had rather than printing the fragment.
    value: () => (isKnownCode(pool, input.value) ? normaliseCode(input.value) : ""),
    ok() {
      const fine = isKnownCode(pool, input.value);
      setText(error, fine ? "" : S.transaction.error.currencyInvalid());
      show(error, !fine);
      return fine;
    },
  };
}

/**
 * A currency cell in a row of the Transaction editor: the same picker, with no
 * refusal of its own to draw. A Transaction happens in ONE currency
 * (spliff/docs/adr/0001), so a cell does not hold a code of its own — it
 * reports the one that was picked and the page writes it into every other cell
 * and into the header.
 */
export function bindCurrencyCell(input: HTMLInputElement, picked: (code: string) => void): void {
  const tell = (): void => {
    if (isKnownCode(pool, input.value)) picked(normaliseCode(input.value));
  };
  // Every keystroke, because the pick from the list arrives as one too: the
  // moment what is typed IS a code, the bill is in it.
  suggest(input);
  input.addEventListener("input", tell);
  input.addEventListener("blur", tell);
}

// The shared half of both bindings: the filtered list, and the spelling. What
// is sent is a code, so what is shown ends up spelled like one — but only once
// it IS one, or "dollar" would turn into "DOLLAR" under the hands of somebody
// halfway through typing the name.
function suggest(input: HTMLInputElement): void {
  autocomplete(input, (q) => currencyChoices(pool, q));
  input.addEventListener("blur", () => {
    if (isKnownCode(pool, input.value)) input.value = normaliseCode(input.value);
  });
}
