---
status: accepted
date: 2026-08-19
---

# Shared plumbing lives in the layer below the apps, and the kit may import dopecore

The 18 Aug 2026 architecture review found the two apps keeping the same program
twice in four places: the Telegram login handshake (xy `auth.go` and dope
`auth.go`, same statuses, same SQL, the same comments word for word, three
behaviour divergences already), and the plumbing around the kit — a 25-line
asset config naming the kit's four disk paths, a 55-line compile-and-cache for
`.dopeui` pages, a 135-line page-contract test and a 120-line
`/admin/create_users` page. dope's copy of the contract test had gone dead: its
script regex predated `dist/`, so it inspected only the kit's `login.js`.

The copies existed because no layer could hold them. `dopecore` may not import
the kit, and the kit imported nothing, so anything that needed both kit
knowledge and app-facing behaviour had nowhere to go but into each app.

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
- The divergences were reconciled on purpose, not preserved: both apps now
  normalise the code (trim, uppercase), retry a code collision on mint,
  refresh `telegram_username` and `telegram_name` on a known telegram's login,
  prove a password through `authcred.VerifyPasswordUpgrading` (identical for
  bcrypt rows; xy has no legacy rows), treat an unparsable `expires_at` as
  expired, name the account's username in every answer (else the telegram
  username on a poll, the claimed one on a claim — `username_taken` and
  `password_required` now carry it in dope too; the page ignores it), and
  mint the session inside the module (both apps' minters were one-line
  delegates to `authcred.CreateSession`). The expiry/replay matrix is tested
  once, on an in-memory SQLite with a map-backed `Users`; the adapters' SQL
  stays under each app's HTTP tests.

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
- The `Users` interface is as wide as the two tables' differences (dope's
  `is_system`, `password_salt`). A third app would implement the same four
  methods; a schema the interface cannot express is the signal to widen the
  interface, not to copy the state machine back out.
