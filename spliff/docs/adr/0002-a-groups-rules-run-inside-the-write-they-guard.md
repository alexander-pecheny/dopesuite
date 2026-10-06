# A Group's rules run inside the write they guard

CONTEXT.md states two invariants: every Share and every Payment names a
Member who is in the Group, and nobody leaves while their Net balance is not
zero. Until 6 Oct 2026 both were checked in the handlers before the write
lock was taken. A member could be removed while a Transaction naming them
was being recorded, and both would pass. An edit could also record a stale
`before` in History.

## Decision

- `spliff/spliff/domain/group` holds every Group rule: record, edit, delete
  and restore, photos, removing a member, leaving, handing over, deleting the
  Group, creating it, renaming it and adding a Phantom. Each rule reads the
  members, the balances and the History `before` through the transaction it
  writes in, and refuses with a User Error. A handler decodes, runs the rule
  inside the write, and encodes.
- `domain/group` is therefore not pure, unlike `ledger`, `split` and
  `money`. It takes the caller's transaction.
- The rate Book is loaded before the transaction opens, because loading it
  can fetch over the network.
- Restoring a deleted Transaction that names a former Member is refused.

## The split and the currency table

- Go is the reference for the even split as the editor shows it: odd minor
  units go first to the payers, in descending Payment (`split.PayerOrder`).
  `testdata/even_cases.json` is read by a Go test and a deno test.
  `split.ByPercent` is deleted, because nothing called it and the editor has
  no percentage split.
- The server does not recompute a submitted split. It stores the amounts it
  is sent.
- `web/ts/money_exponents_gen.ts` is generated from `currencies.go`, and a
  test fails when it is out of date. It is generated rather than served from
  `/api/currencies`, because amounts are formatted before that request could
  answer. A shared parse and format fixture brought Go's `Parse` in line with
  the editor.
