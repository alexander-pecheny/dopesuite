# Codebase Map

## What This Is

Expense sharing, shaped like Splitwise. People in a **Group** record who paid
for whom, in any currency, and the Group always knows who owes whom. The terms
are defined in [`CONTEXT.md`](CONTEXT.md): Group, Transaction, Payment, Share,
Unclaimed, Rate table, Rate date, Debt graph, Owner, Member, Former Member,
Phantom, Invite Link, History and Photo. Use those words everywhere. What the product should do is
described in [`docs/spec-v1.md`](docs/spec-v1.md).

**This module is English only.** There is no `ru/` catalog, and `just
cyrillic-check` must find nothing at all under `spliff/`. The Default language
entry in the root `CONTEXT.md` says as much.

## Stack

- **Backend**: Go 1.26, SQLite (WAL, `modernc.org/sqlite`, pure Go, no cgo).
- **Frontend**: strict-TypeScript ES modules (root ADR-0001), sources in
  `spliff/web/ts/`, bundled by the root `just build-web spliff` into the
  gitignored `static/dist/` the Go build embeds. Pages are `.dopeui` sources
  compiled through `kit.PageSet`; the design system is DopeUIKit.
- **Frontend tests**: deno (`deno test --parallel spliff/web/jstest/`).
- **Deploy**: `just deploy` → the monorepo's `../deploy.py`, targets
  `spliff-server` and `splifftest`, host `vps-he`.

Spliff is one module of the **dopesuite** monorepo; the module root is this
`spliff/` directory (`go.mod: module "spliff"`), not the repo root. See the root
`AGENTS.md` for the monorepo rules.

## Directory Structure

The Go code uses the same groups as dope, where they apply. There are no loose
`.go` files under the inner `spliff/`.

```
spliff/                  module root (go.mod: module "spliff")
  i18nstrings/           the Catalog: en/*.toml + the generated Strings
  spliff/                server tree — packages resolve as spliff/spliff/<group>/<pkg>
    cmd/spliff-server/   thin main() → spliffserver.Main()
    server/              package spliffserver — the trunk + server/tests/ (integration)
    web/                 route (the dispatcher), assets (embed), ui (the kit overlay), ts, jstest
    domain/              money, rates, split, ledger — pure, and tested first;
                         group — the Group's rules, run inside the write transaction
    storage/store/       the schema as a migration list, and every query
    platform/imagex/     what happens to a Photo between the phone and the disk
  deploy/                the systemd unit, as an example to copy
```

## Key Files

| File | Purpose |
|------|---------|
| `spliff/domain/money` | An `Amount` is an integer number of minor units together with an ISO 4217 code. Also the exponent table (JPY has 0, KWD and five others have 3, everything else has 2), and parsing and formatting. No float ever touches an amount. The browser's exponent table, `web/ts/money_exponents_gen.ts`, is generated from `currencies.go` (`SPLIFF_UPDATE_EXPONENTS=1 go test ./spliff/domain/money`), and `testdata/amount_cases.json` holds the parsing and formatting cases that Go and `jstest/money-parity.test.js` both check |
| `spliff/domain/rates` | The Rate table, `ResolveDay` (which picks the nearest day, and the earlier one if two are equally near), and `Convert`, which is the ONLY conversion function. It rounds half-even and takes a Pinned rate argument that v1 never sets |
| `spliff/domain/split` | Turns an even split into the amounts that actually get stored. It uses largest remainder, and gives the leftover minor units to the payers in order of descending Payment (`PayerOrder`), then in split order. It is the reference for the editor's copy in `web/ts/txform.ts`: both test against `testdata/even_cases.json`. The server does not recompute a submitted split, because only amounts are stored |
| `spliff/domain/group` | The Group's rules: recording, editing, deleting and restoring a Transaction (with its History entry), attaching Photos, removing Members, handing over and deleting the Group. Each function takes the write transaction and reads what it checks (Members, Net balances, the Transaction as it stood) through it, so no other write can land between the check and the write. Refusals are User Errors. Not pure: it goes through `storage/store` |
| `spliff/domain/ledger` | Net balances and the greedy Debt graph. Conversion is kept exact the whole way through and the column is rounded only once at the end, so the balances add up to exactly zero. `Balances` asserts that |
| `spliff/storage/store/schema.go` | the schema as `[]schema.Migration`; `server/tests/testdata/schema.sql` pins what the list makes of an empty file (`SPLIFF_UPDATE_SCHEMA=1` regenerates) |
| `spliff/web/route/route.go` | **The dispatcher.** A route declares the access it needs (Public, LoggedIn, Member or Owner), and the table resolves the session, the Group and the Owner before the handler runs. The same-origin check on writes is here too |
| `spliff/server/routes.go` | the whole route table, one row per endpoint |
| `spliff/server/groups.go` | the Group page's read (balances, Debt graph, feed), and the handlers for the Group's settings, leaving and kicking. Each write handler decodes, runs a `domain/group` function inside `withWriteTx`, and encodes |
| `spliff/server/transactions.go` | The Transaction handlers: decode, `domain/group` inside one write transaction, then the DMs the rules call for |
| `spliff/server/invites.go` | The adapter over `dopecore/invitelink`. This file supplies Spliff's tables, its peek and join responses, the Phantom claim and the wording (`inviteTexts`); the state machine, the request bodies, the list's wire shape and the error mapping belong to that package |
| `spliff/server/rates.go` | The fetcher. It pulls one table a day from open.er-api.com, both when a rate is first needed and on a daily ticker. If a fetch fails, the nearest table already stored stays in use |
| `spliff/server/auth.go` | the `dopecore/tglogin` adapter and password login |
| `spliff/server/testapi.go` | the single exported test seam for `server/tests/` |
| `spliff/web/ts/txform.ts` | The editor's model, with no DOM in it. It is the browser's copy of `domain/split`, and it has to agree with it down to the minor unit; `jstest/split-parity.test.js` checks that against Go's fixture |

## Rules that are not obvious

- **An amount is never stored converted** (`docs/adr/0001`). A Transaction keeps
  the currency it was entered in, permanently. The Base currency is only a
  display setting, and changing it just re-reads everything.
- **Every write to a Transaction appends to History.** The app is usable because
  everyone can edit anything, and it is auditable because History records who
  did.
- **Nobody leaves owing.** Leaving a Group, being removed from one and deleting
  one are all refused while any balance is non-zero, and the refusal says how
  much. That is what stops the Debt graph from pointing at someone who is no
  longer there. Restoring a deleted Transaction that names a Former Member is
  refused for the same reason.
- **Leaving keeps the member row** (`docs/adr/0003`). Removing a Member stamps
  `group_members.left_at` instead of deleting the row, so an old Transaction or
  History entry can still name them. `store.Members`, `MemberByID`, `Phantoms`,
  `IsMember` and `GroupsOf` read current Members only; `store.AllMembers` is
  for looking up names. A Former Member who joins again gets the same row back.
- **A Group rule is checked inside the write it guards.** Read the Members,
  the balances or the Transaction's "before" through the `tx` the write runs
  in, never through `s.db` ahead of `withWriteTx`. Load the rate `Book` before
  the transaction opens, because loading it can fetch over the network.
- **A Member is a ROW, not an account.** `transaction_payments.member_id` and
  `transaction_shares.member_id` both point at a row in `group_members`, and the
  account behind that row may be null. A row with no account is what we call a
  Phantom. Three things follow. The `member_id` in the API is never a user id.
  Claiming a Phantom is a single UPDATE of a single row, so nothing is
  re-pointed and no balance can move. And `group_members.id` is AUTOINCREMENT,
  so the id of a removed row is never given to somebody else. History's
  `actor_id` is still a user id, because a Phantom cannot do anything.
- **There is one Payment and one Share per Member per Transaction.** An edit
  describes what the Transaction now IS, so the entries are replaced entirely
  rather than patched.
- **A Photo is always re-encoded, never stored as it arrived.** The EXIF that a
  phone writes records where and when the receipt was photographed.
- **There are exactly three DMs, and no settings page for them.** They are
  best-effort, and a failure to send one never blocks a write.

## How to Run / Build / Test

```bash
just dev          # server (assets hot-read from disk); polls telegram if SPLIFF_BOT_TOKEN is set
just test         # go test + deno frontend tests
just check        # this module: fmt + vet + tidy-check + test
just pre-commit   # the whole repo, incl. class-check — run before a commit
printf 'secret12' | just adduser someone   # a password account, for an instance with no bot
```

Server listens on `$PORT` (default 9676); database at `$SPLIFF_DB` (default
`spliff.db`), Photos under `$SPLIFF_BLOBS` (default `blobs`). Config via `.env`
(copy from `.env.example`).

## UI

Use DopeUIKit and nothing else. Read `dopeuikit/README.md` and `DESIGN.md`
before you write a page. Look for a kit class before you add one, and never add
a class whose whole body is layout utilities, because `scripts/classcheck` will
reject it. Spliff's own CSS layer is `spliff/web/assets/static/styles.css`, and
it is deliberately small: how an amount is set, and the two shapes a bill takes
on screen.

Design for mobile first. People enter bills at the table.

Run the `design-review` skill after building any surface and the `verify` skill
to drive it in a browser at both sizes.
