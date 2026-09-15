# Codebase Map

## What This Is

Splitwise-shaped expense sharing: people in a **Group** record who paid for
whom, in any currency, and the Group always knows who owes whom. The terms are
in [`CONTEXT.md`](CONTEXT.md) — Group, Transaction, Payment, Share, Unclaimed,
Rate table, Rate date, Debt graph, Owner, Member, Invite Link, History, Photo —
and they are the words to use everywhere; the product shape is
[`docs/spec-v1.md`](docs/spec-v1.md).

**English only.** There is no `ru/` catalog and `just cyrillic-check` must find
nothing under `spliff/`. Root `CONTEXT.md`'s Default language says so.

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

The Go code is organised into the semantic groups dope uses, where they apply
(no loose `.go` files under the inner `spliff/`):

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
| `spliff/domain/money` | `Amount` = integer minor units + an ISO 4217 code, the exponent table (JPY 0, KWD and its five siblings 3, the rest 2), parse and format. No float ever touches an amount |
| `spliff/domain/rates` | the Rate table, `ResolveDay` (nearest day, the earlier one on a tie), and `Convert` — the ONE conversion function, half-even, carrying the Pinned rate argument v1 never sets |
| `spliff/domain/split` | an even or percentage split becoming the amounts that are stored: largest remainder, spare minor units to the payers by descending Payment and then in split order |
| `spliff/domain/ledger` | Net balances and the greedy Debt graph. Conversion stays exact all the way and the whole column is rounded once, so the balances sum to exactly zero — which `Balances` asserts |
| `spliff/storage/store/schema.go` | the schema as `[]schema.Migration`; `server/tests/testdata/schema.sql` pins what the list makes of an empty file (`SPLIFF_UPDATE_SCHEMA=1` regenerates) |
| `spliff/web/route/route.go` | **the dispatcher**: a route states the access it asks (Public / LoggedIn / Member / Owner) and the table resolves the session, the Group and the Owner before the handler runs. The same-origin check on writes lives here |
| `spliff/server/routes.go` | the whole route table, one row per endpoint |
| `spliff/server/groups.go` | the Group page's read (balances, Debt graph, feed) and the Group's own settings, leaving and kicking |
| `spliff/server/transactions.go` | every write to a Transaction: validate, write, append History, knock on the doors the DM rules name |
| `spliff/server/invites.go` | the adapter over `dopecore/invitelink` — Spliff's tables, DTOs and words; the machine is the package's |
| `spliff/server/rates.go` | the fetcher: one table a day from open.er-api.com, on first need and by a daily ticker; a failed fetch leaves the nearest table in use |
| `spliff/server/auth.go` | the `dopecore/tglogin` adapter and password login |
| `spliff/server/testapi.go` | the single exported test seam for `server/tests/` |
| `spliff/web/ts/txform.ts` | the editor's model, with no DOM in it: the browser's copy of `domain/split`, and it must agree with it minor unit for minor unit |

## Rules that are not obvious

- **Amounts are never stored converted** (`docs/adr/0001`). A Transaction keeps
  its own currency forever; the Base currency is a knob, and changing it is a
  re-read.
- **Every write to a Transaction appends History.** Full trust is what makes the
  app usable; History is what makes it auditable.
- **Nobody leaves owing.** Leaving, being kicked and deleting a Group are all
  refused while a balance is non-zero, and the refusal names the amount. That is
  what keeps the Debt graph from naming a ghost.
- **One Payment and one Share per Member per Transaction.** An edit states what
  the Transaction IS, so the entries are replaced whole.
- **A Photo is re-encoded, never stored as sent.** The EXIF a phone writes
  carries where and when the receipt was photographed.
- **The DMs are three, and there is no settings page for them.** They are
  best-effort and never block a write.

## How to Run / Build / Test

```bash
just dev          # server (assets hot-read from disk); polls telegram if SPLIFF_BOT_TOKEN is set
just test         # go test + deno frontend tests
just check        # this module: fmt + vet + tidy-check + test
just pre-commit   # the whole repo, incl. class-check — run before a commit
printf 'secret12' | just adduser someone   # a password account, for an instance with no bot
```

Server listens on `$PORT` (default 9674); database at `$SPLIFF_DB` (default
`spliff.db`), Photos under `$SPLIFF_BLOBS` (default `blobs`). Config via `.env`
(copy from `.env.example`).

## UI

DopeUIKit only. Read `dopeuikit/README.md` and `DESIGN.md` before writing a
page; reuse a kit class before adding one, and never add a class whose body is
only layout utilities (`scripts/classcheck` refuses it). Spliff's own CSS layer
is `spliff/web/assets/static/styles.css` and is deliberately small: how an
amount is set, and the two shapes a bill takes on screen.

Mobile-first: the audience enters bills at the table.

Run the `design-review` skill after building any surface and the `verify` skill
to drive it in a browser at both sizes.
