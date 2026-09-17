---
status: accepted
date: 2026-08-19
---

# Shared plumbing lives in the layer below the apps, and the kit may import dopecore

The architecture review on 18 August 2026 found four places where the two apps
were keeping the same program twice.

One was the Telegram login handshake, in xy's `auth.go` and dope's `auth.go`.
They had the same statuses, the same SQL and the same comments word for word,
and their behaviour had already diverged in three ways.

The other three were the plumbing around the kit: a 25-line asset config naming
the kit's four paths on disk, a 55-line compile-and-cache for `.dopeui` pages, a
135-line page-contract test, and a 120-line `/admin/create_users` page. dope's
copy of the contract test had quietly stopped working: its regex for finding
scripts was written before `dist/` existed, so it was only inspecting the kit's
`login.js`.

These copies existed because there was no layer that could hold them.
`dopecore` is not allowed to import the kit, and the kit imported nothing at
all, so anything that needed both kit knowledge and app-facing behaviour had
nowhere to live except inside each app.

## Decision

- **The kit imports dopecore.** The layering is dopecore ← dopeuikit/kit ←
  apps; `dopeuikit/ui` (the engine) still imports nothing, and dopecore
  imports no other module. App-facing plumbing that needs kit knowledge lives
  in `kit/` (or a package beside it): `kit.Assets`, `kit.PageSet`,
  `kit.AdminCreateUsers`, `uitest.PageContract`. Each app keeps its
  allow-lists and its page chrome, ≈25 lines each.
- **The Telegram handshake has one home: `dopecore/tglogin`.** `Start`,
  `Resolve` and `Claim` are transactional steps over a `Tx` the app opens and
  a `Users` interface the app implements (four methods: `ByTelegram`,
  `ByUsername`, `Create`, `Attach`). The module owns the SQL on
  `telegram_login_codes`, the reap, the mint with collision retry, the
  expiry-bounded replay guard, the session mint and the closed status set the
  shared login page reads. Each app keeps its write lock, its username
  validation and its error text; an app refuses a username by returning its
  own error from `Create`, which `Claim` passes through.
- We reconciled the divergences deliberately instead of preserving them. Both
  apps now do all of the following. They normalise the code by trimming it and
  upper-casing it. They retry when minting a code collides with an existing one.
  They refresh `telegram_username` and `telegram_name` when a telegram account
  they already know logs in. They check a password with
  `authcred.VerifyPasswordUpgrading`, which behaves identically for bcrypt rows,
  and xy has no legacy rows anyway. They treat an `expires_at` they cannot parse
  as expired. They name the account's username in every answer, falling back to
  the telegram username on a poll and to the claimed one on a claim; dope now
  carries it on `username_taken` and `password_required` too, although the page
  ignores it. And they mint the session inside the module, since both apps'
  minters were one-line delegates to `authcred.CreateSession`. The matrix of
  expiry and replay cases is tested once, against an in-memory SQLite with a
  map-backed `Users`. The SQL in each adapter is still covered by that app's own
  HTTP tests.

## Consequences

- A kit asset rename breaks nothing silently: the disk paths are written once.
- dope's five game pages are back under the selector contract, and any new
  script-lookup pattern (the next `modal("stem")`) is taught to the contract
  in one place.
- `dopecore` gained `modernc.org/sqlite` as a test dependency; both apps
  already carried it.
- `/admin/create_users` looks the same in both apps: the body is dope's
  (`empty` rows for skipped/errors where xy had `hint`, and the
  `data-select-all` attribute, inert without dope's `pageforms.js`).
- The `Users` interface is exactly as wide as the differences between the two
  tables, which are dope's `is_system` and `password_salt`. A third app would
  implement the same four methods. If a schema turns up that the interface
  cannot express, widen the interface; do not copy the state machine back out
  into the app.

## Addendum (19 Aug 2026, second review, K1)

The session read was the other half of what `tglogin` settled: `authcred`
owned `CreateSession` and `NeedsRefresh`, the apps each kept the 40–57-line
cookie-to-user lookup (same join, same expire-and-delete, same slide), and
xy still proved a password with `VerifyPassword` where dope used
`VerifyPasswordUpgrading`. `authcred.Sessions{UserColumns, UserDest}.Lookup` is now the single statement.
The app tells it which columns its own users table has, such as dope's
`is_system`, and where to scan them into. It answers with the user, a state
(`NoSession`, `Expired` or `Live`), and whether the session is due to be slid
forward. `SlideSession` and `DeleteSession` are the two writes, and each app
runs them under its own write discipline: xy's `withWriteTx`, dope's direct
exec. Both apps check a password with `VerifyPasswordUpgrading`, and xy passes
an empty salt.

## Addendum (19 Aug 2026, second review, H4)

The migration runner was the last piece of SQLite plumbing one app could not
import: dope's `storage/schema.Apply` (A6) lived in the dope module, so xy
kept the guard and the record footer copied into 21 `migrateVN` functions and
a newest-first chain that nothing tested. `pecheny.me/dopecore/schema` is the
runner now — `Apply(db, []Migration)` validates the order, runs each step
once and records it; `Exec(script)` is the Up of a plain SQL step. xy's
`internal/server/db.go` is the list, ascending, same numbers and same SQL;
a fresh database from the old chain and the new list have identical
`sqlite_master`. `XY_REHEARSE_DB` walks a prod snapshot through the list the
way `DOPE_REHEARSE_DB` does.

## Addendum (19 Aug 2026, second review, K4)

Both bots re-implemented the same classify-and-reply machine over `tgbot`'s
transport, and had drifted: dope's had no health endpoint, xy's posted junk
to its server, and a bare `/start` greeted in one and asked the server in the
other. `tgbot.LoginHandler(bridge, Texts{Help, Down})` is the one
conversation: a code (pasted or in a `/start` deep link) registers through
the server; `/login`, a bare `/start` and any other command ask the server
for its «go to the site» reply; text that cannot be a code gets the app's
`Help` without a round trip; an unreachable server gets `Down`. Each `main.go`
keeps its env names and its two texts. dope's bare `/start` now answers with
the server's text (the same instruction its greeting carried), sent plain.

## Addendum (15 Sep 2026, before Spliff: `dopecore/invitelink`)

Spliff's Groups admit people in exactly the same way xy's boards do: through a
link the owner creates, which may limit how many times it is used, expire, or
hold the joiner until the owner approves them. So the 617 lines of
`xy/internal/server/boardinvites.go` were about to be copied, which would have
made it the third piece of plumbing in a row. `pecheny.me/dopecore/invitelink` is the machine:
the two tables' SQL, the state ladder (`active`/`revoked`/`expired`/
`exhausted`, overridden per caller by `member`/`pending`/`declined`/`spent`),
the rule that only a use which reached `joined` spends a seat, the scope-wide
one-person-one-request invariant, and the mint/revoke/delete/decide/peek/join
steps over a `Tx` the app opens.

Two things the adapter supplies, because they are not the machine's. The table
names — `Links{Invites, Uses, ScopeID, ScopeJoin}` — since xy keeps
`board_invites`/`board_invite_uses` and its v23 schema unchanged (no migration:
the SQL was already shareable, only the identifiers were xy's). The second is `Scope`, which is what a board or a Group actually is. It
provides `IsMember`, `AddMember`, `DisplayName` (which is empty for a legacy xy
board whose name is still ciphertext, since an empty name is better than a wrong
one), `Names` for the list of who joined, and `NudgeOwner`, which is called
after the commit so that nobody is told about a join that was then rolled back.

`ScopeJoin` is worth spelling out. A link is resolved from its own table, so
without a join that excludes deleted boards, the links belonging to a deleted
board would carry on working. xy already had a test for that, and the test now
belongs to the package.

Strings stay where `tglogin` put them. The package answers with a `State`, or
with one of five sentinel errors — `ErrNotFound`, `ErrRequestNotFound`,
`ErrNoSeatsLeft`, `ErrLimitsOutOfRange`, `ErrLabelTooLong` — or with a
`*Refused` that carries the state. xy's adapter maps each of those to its own
status and its own catalog entry, so `inviteRefusal` still reads the same six
Russian lines it always did. xy's HTTP
handlers are thin adapters now, and `boardinvite_test.go` passed unchanged
through the whole move — it is the gate the extraction was checked against.
The package's own tests are the tglogin shape: an in-memory SQLite holding the
two tables under names no app uses, and a map-backed `Scope`.
