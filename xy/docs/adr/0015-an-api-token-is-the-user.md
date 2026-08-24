# An API token is the user

## Status

Accepted, 2026-08-24.

## Context

xy's API has always been cookie-only. API tokens existed, but authorized just
the three Trello-compatible routes chgksuite calls — a bearer credential that
could read a board's ciphertext and post a card, and nothing else.

`xy-cli` (ADR-0016) needs an unattended client: an agent running commands with
nobody at the keyboard. The options were a session cookie kept in a file (a
credential that is indistinguishable from the human's own browser session,
minted by handing the CLI the account password) or teaching the API to accept
the tokens it already mints, which are month-lived, listed at
`/profile/tokens`, and revocable one by one.

## Decision

`lookupSession` resolves `Authorization: Bearer <api_token>` against
`api_tokens` — hash compare, expiry and revocation checked, `last_used_at`
stamped — and the resulting user is the token's owner. **A token is the user**:
every route a cookie reaches, a token reaches, with three exceptions guarded by
`requireCookieUser`:

- `POST /api/auth/password` — the kill switch below,
- `POST /api/auth/username` — the handle mentions resolve against,
- `/admin*` — which creates accounts.

**Changing the password is the kill switch.** It now revokes every API token of
the account and deletes every session but the caller's own. One act the user
already knows how to perform ends every leaked credential, including tokens an
attacker minted along the way — which is why the password route is exactly the
one a token may not reach.

The consequences chosen deliberately:

- **A leaked token is a full compromise of that account's boards** — it can
  read, write and delete everything the user can, and mint sibling tokens. This
  is the price of one simple rule instead of a scope system; the answer to a
  leak is the kill switch, not a narrower grant.
- **A token still cannot decrypt anything.** It authorizes the API; content is
  E2E-encrypted under the board passphrase, so a stolen token yields ciphertext
  plus the plaintext board names, exactly as a stolen session cookie does.
- **No scopes, no read-only tokens.** They would need a permission model on
  every route, and an agent that may write cards but not comments is not a
  distinction anyone has asked for.

## Alternatives considered

**A stored session cookie.** No server change, but the CLI would have to be
given the account password to mint one, sessions are not listed or revocable
individually, and a leak looks exactly like the user's own browser.

**Scoped tokens (per board, read-only).** Real defence in depth, and a real
permission model to build and keep correct on every route. Deferred: the kill
switch covers the leak case, and scopes can be added later without changing
what an unscoped token means.
