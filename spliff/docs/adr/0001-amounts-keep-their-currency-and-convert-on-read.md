---
status: accepted
date: 2026-09-15
---

# Amounts keep their own currency and convert on read through per-day rate tables

A Transaction is stored as integer minor units in the currency it happened in,
plus its date. The server fetches one full rate table a day from
open.er-api.com (USD base, ~160 currencies, no history on the free endpoint)
and keeps every table it ever fetched. Every conversion — a Share into the
Group's base currency, a Net balance, the Debt graph — happens when read, using
the table nearest to the Transaction's date (the earlier one on a tie). Nothing
converted is ever written.

The alternative was the Splitwise-shaped one: snapshot a rate onto each
Transaction at entry, and let a person pin the rate their bank really charged.
That makes a second source of truth per Transaction, and it makes changing a
Group's base currency a rewrite instead of a re-read. Per-day tables cost 160
rows a day, make the base currency a free knob, and make a backdated
Transaction get whatever table is nearest rather than a fetch we cannot make.

## Consequences

- A person who backdates by weeks gets a stale-ish rate; that is on them.
- A pinned per-Transaction rate is a planned escape hatch, not a v1 feature,
  and the design leaves room for it: a pin is an optional (to-currency, rate)
  pair on the Transaction, and conversion is one function — pin first if it
  names the target, else the day's table; if the pin names some other currency
  the amount goes through the pin and then through the table for the rest of
  the way. So a pin survives a base-currency change and never rewrites data.
  Until it ships, a person who wants the bank's rate enters the amount in the
  base currency instead.
- Settling a EUR-based Group in GEL a month later leaves a few cents of drift
  in the Net balances. Accepted; the greedy graph absorbs it like any other
  amount.
