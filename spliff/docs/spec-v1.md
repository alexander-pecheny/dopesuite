# Spliff v1 — what was decided on 2026-09-15

The grilling session's outcome. Terms are defined in `spliff/CONTEXT.md`; this
file is the product and engineering shape, not the glossary. Anything not here
is out of v1 (categories, recurring expenses, comments, CSV export, cross-Group
summaries); the per-Transaction pinned rate is out of v1
but designed for, see Rates.

## Where it lives

- Fourth module of the dopesuite monorepo: `spliff/` (`go.mod: module
  "spliff"`), on `dopecore` + `dopeuikit` via `replace ../`, laid out like
  `dope/` (inner `spliff/` tree with cmd/server/web/domain/storage/platform).
- Its own SQLite file, its own bot token (`SPLIFF_BOT_TOKEN` is the switch,
  root ADR-0005), its own blob dir, a `spliff-server` (+ `splifftest`) row in
  `deploy.py` on host `vps-he`. Public URL from `SPLIFF_PUBLIC_URL`, same rule
  as `XY_PUBLIC_URL` (required outside development).
- Root `justfile` fans `test`/`fmt`/`vet`/`generate-strings`/`strings-check`
  out to it; `CONTEXT-MAP.md` lists it.

## Language

English only. No `ru/` catalog. This needs (1) a per-module default-language
knob in `scripts/i18nstringsgen` (today `const defaultLang = "ru"`) and in the
TypeScript emitter; (2) an `en/` copy of the kit's catalog (`dopeuikit/
i18nstrings/`) so the shared login page and common words render in English;
(3) root `CONTEXT.md`'s Default language reads "Russian unless the module says
otherwise". `just cyrillic-check` should find nothing in `spliff/`.

## Preparatory extractions (separate PRs, before Spliff code)

1. **Invite Links → `dopecore/invitelink`.** Lift xy's `boardinvites.go`
   (mint, use cap, expiry, approval + Join Requests, revoke, peek, join, the
   who-came-in list) into a package over a `Tx` and a small scope interface
   (is member, add member, display name, nudge), the way `tglogin` was done
   (root ADR-0004). xy adapts; its invite tests stay green and are the gate.
   Keep xy's SQL shape; the table name is the adapter's business.
2. **`xy/internal/blobstore` → `dopecore/blobstore`.** Content-agnostic
   already; xy changes an import path.
3. **Login redirect**: the shared login page already honours
   `data-login-redirect`. Each app's login route may stamp it from a `next`
   query param, accepted only as a same-origin absolute path.

## Accounts

Open registration through the shared Telegram handshake (`dopecore/tglogin`;
Spliff implements the four-method `Users` interface). An anonymous visitor at
an Invite Link sees the Group's name and a "log in with Telegram" button, comes
back to the link via `next`, and joins in one tap.

## Groups and Members

- Phantoms (added 16 Sep 2026, see CONTEXT.md): a Member row with a display
  name and no user. Owner-only to add. Same table as Members so every Payment
  and Share still names a Member; the Debt graph lists it by name. A joiner at
  an Invite Link may pick "I am <phantom>"; claiming re-points the Phantom's
  membership, Payments, Shares and History rows to the joiner's account in one
  transaction (refused if the joiner is already a Member). Removal follows the
  zero-balance rule.

- A Group: name, base currency (any ISO 4217 the rate table carries), Owner,
  Members, Invite Links.
- Owner: kick Members, mint/revoke Invite Links, decide Join Requests, delete
  the Group, hand ownership to another Member; cannot leave without handing
  over.
- Every Member: every field of every Transaction, the Group's name and base
  currency, leaving.
- Leaving or being kicked is refused while the Member's Net balance is
  non-zero (User Error naming the amount). So every Payment and Share always
  names a current Member.

## Transactions

One model, the UI has modes (simple "A paid X for B", split, "I paid, claim
your part", settlement). Fields:

- Group, description (free text), date (the expense date, default today),
  currency, total in minor units.
- Payments: Member → amount, one or more, summing to the total.
- Shares: Member → amount, zero or more, at most one per Member, summing to at
  most the total. Only amounts are stored; percentages and even splits are
  form conveniences.
- Unclaimed = total − ΣShares ≥ 0. Enforced on every write; a Claim that
  overdraws is refused with a User Error naming what is left.
- Photos: zero or more, via blobstore.
- Rounding of derived splits: largest-remainder; leftover minor units go one
  each to the payers in descending Payment amount, then the rest in split
  order. Deterministic so two people see the same numbers.
- Money: integer minor units + ISO 4217 code + a per-currency exponent table
  (JPY 0, most 2, KWD/BHD/… 3). Rates are decimal strings, never floats.

Settlement is a Transaction with one Payment and one Share, whole amount each.

## Editing, History, deletion

- Full trust: any Member edits or deletes any Transaction.
- History: every change to a Transaction — who, when, what (field-level diff
  is fine; at minimum before/after of payments, shares, total, currency, date,
  description, photos) — visible to every Member, per Transaction and as a
  Group feed.
- Delete is soft: the Transaction leaves the ledger, stays in History as a
  deleted entry any Member can restore with shares and photos intact. No
  reaping in v1.

## Rates

- Source: `https://open.er-api.com/v6/latest/USD`. One fetch per calendar day
  (UTC), stored as a Rate table: day + currency → rate against USD. Fetch on
  the first request that needs today's table and lacks it, plus a daily timer;
  a failed fetch leaves the previous table in use (nearest-day rule).
- Rate date for a Transaction: its own date if a table exists for it, else the
  nearest day with one, earlier on a tie. Shown on the form as "rate as of".
- Conversion X → base on day D: amount × (rate_base / rate_X) from D's table,
  rounded half-even to the base currency's minor units.
- Amounts are never stored converted (`spliff/docs/adr/0001`).
- **Design for the pinned rate now, ship it later.** All conversion goes
  through one function, `convert(amount, from, to, rateDate, pin?)`, where a
  pin is an optional (to-currency, decimal rate) on the Transaction. The rule
  is: a pin naming `to` is used as-is; a pin naming another currency Y converts
  to Y through the pin and Y → `to` through the day's table; no pin means the
  day's table. v1 stores no pin (no column, no form field) but the domain
  type, the function and its tests already carry the optional argument, so
  adding it is a migration and a form field, not a redesign.

## Net balance and Debt graph

- Net balance per Member, in base minor units: Σ Payments − Σ Shares −
  absorbed Unclaimed, where a Transaction's Unclaimed is absorbed by its payers
  pro rata to their Payments (largest remainder), each converted at that
  Transaction's rate date. Deleted Transactions do not count. Balances sum to
  zero by construction — assert it.
- Debt graph: greedy. Sort creditors and debtors by |balance| desc, ties by
  join order; pair largest with largest, transfer the min, repeat. ≤ n−1
  transfers, a pure function of the ledger, computed on read, never stored.
- Shown on the Group page: each Member's balance and the transfer list ("B
  pays A 43.10 EUR").

## Telegram DMs (three, no settings page)

1. Join Request → the Owner (as xy does).
2. A Transaction with Unclaimed > 0 is created → every other Member.
3. A Transaction where you hold a Payment or a Share is created or edited by
   someone else → you. One DM per Transaction per person per change.
Messages come from the catalog and carry the Group name and a link built from
`SPLIFF_PUBLIC_URL`.

## Photos

- Upload cap 15 MB before decoding; images only; re-encode with Go's image
  package to JPEG, long side ≤ 2000px (drops EXIF/GPS). Store via blobstore;
  the DB keeps the ref, uploader, time, transaction.
- Served to Members only through the session.
- Backup: litestream for the DB, the same hourly `rclone sync --backup-dir`
  recipe xy uses for blobs; write it into the deploy notes.

## UI

- Kit primitives only (`design-review` skill after every surface; `verify`
  skill drives it in Chrome). Pages: groups list, group (balances, debt graph,
  transactions feed, members, invite links for the Owner), transaction
  (view/edit with the modes above, photos, history), invite landing, login.
- Mobile-first: the audience enters bills at the table.

## Out of scope in v1

Categories, recurring expenses, comments, CSV export, per-Transaction pinned rates, cross-Group personal summaries, a second
language.
