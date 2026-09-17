# Codebase Map

## What This Is

Expense sharing, shaped like Splitwise. People in a **Group** record who paid
for whom, in any currency, and the Group always knows who owes whom. The terms
are defined in [`CONTEXT.md`](CONTEXT.md): Group, Transaction, Payment, Share,
Unclaimed, Rate table, Rate date, Debt graph, Owner, Member, Invite Link,
History and Photo. Use those words everywhere. What the product should do is
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
    domain/              money, rates, split, ledger — pure, and tested first
    storage/store/       the schema as a migration list, and every query
    platform/imagex/     what happens to a Photo between the phone and the disk
  deploy/                the systemd unit, as an example to copy
```

## Key Files

| File | Purpose |
|------|---------|
| `spliff/domain/money` | An `Amount` is an integer number of minor units together with an ISO 4217 code. Also the exponent table (JPY has 0, KWD and five others have 3, everything else has 2), and parsing and formatting. No float ever touches an amount |
| `spliff/domain/rates` | The Rate table, `ResolveDay` (which picks the nearest day, and the earlier one if two are equally near), and `Convert`, which is the ONLY conversion function. It rounds half-even and takes a Pinned rate argument that v1 never sets |
| `spliff/domain/split` | Turns an even or percentage split into the amounts that actually get stored. It uses largest remainder, and gives the leftover minor units to the payers in order of descending Payment, then in split order |
| `spliff/domain/ledger` | Net balances and the greedy Debt graph. Conversion is kept exact the whole way through and the column is rounded only once at the end, so the balances add up to exactly zero. `Balances` asserts that |
| `spliff/storage/store/schema.go` | the schema as `[]schema.Migration`; `server/tests/testdata/schema.sql` pins what the list makes of an empty file (`SPLIFF_UPDATE_SCHEMA=1` regenerates) |
| `spliff/web/route/route.go` | **The dispatcher.** A route declares the access it needs (Public, LoggedIn, Member or Owner), and the table resolves the session, the Group and the Owner before the handler runs. The same-origin check on writes is here too |
| `spliff/server/routes.go` | the whole route table, one row per endpoint |
| `spliff/server/groups.go` | the Group page's read (balances, Debt graph, feed) and the Group's own settings, leaving and kicking |
| `spliff/server/transactions.go` | Every write to a Transaction: validate it, write it, append to History, and send the DMs the rules call for |
| `spliff/server/invites.go` | The adapter over `dopecore/invitelink`. This file supplies Spliff's tables, DTOs and wording; the state machine itself belongs to that package |
| `spliff/server/rates.go` | The fetcher. It pulls one table a day from open.er-api.com, both when a rate is first needed and on a daily ticker. If a fetch fails, the nearest table already stored stays in use |
| `spliff/server/auth.go` | the `dopecore/tglogin` adapter and password login |
| `spliff/server/testapi.go` | the single exported test seam for `server/tests/` |
| `spliff/web/ts/txform.ts` | The editor's model, with no DOM in it. It is the browser's copy of `domain/split`, and it has to agree with it down to the minor unit |

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
  longer there.
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
