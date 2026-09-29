---
status: accepted
date: 2026-09-29
---

# An API token is the user, and every host form has a JSON twin

## Context

We want an agent to change a fest the way an organizer does: build the games,
fix the roster, number the teams, enter results and undo a mistake. dope had a
JSON API at `/api/fest/…`, but it accepted only a session cookie. It also
covered only live game editing. Everything an organizer does on `/host/…`
(creating a fest or a game, settings, access, teams and their Flags, player
overrides, troikas, numbers, the rating import, the history and revert) was an
HTML form whose answer was a redirect or a page.

xy solved the credential half already (xy ADR-0015, ADR-0016): month-long API
tokens minted on the profile, a token that *is* the user, and a CLI plus a skill
for the agent.

## Decision

- **The same token model as xy.** `api_tokens` (migration v33) holds the sha256
  of each token. Tokens are made and revoked on `/profile` and through
  `GET|POST /api/auth/tokens` and `DELETE /api/auth/tokens/{id}`, and each lives
  30 days. `Engine.LookupSession` accepts `Authorization: Bearer …` before the
  cookie, so a token reaches every route a cookie reaches. That includes the
  audit log: a token's writes are journalled under its owner's name.
- **Three exceptions, as in xy.** A token cannot change the password or the
  username (`/api/auth/password`, `/api/auth/username` answer 403), and it
  cannot reach `/admin`, which reads only the cookie (`LookupCookieSession`).
- **Changing the password is the kill switch.** It revokes every token and ends
  every other session of the account. An admin's password reset link does the
  same.
- **Every host form has a JSON twin in the `/api` table.** `hostpages.APIRoutes`
  registers them with the same access levels as the pages. Each form handler
  was split into a function that does the work and returns a `UserError` for
  anything a person should read (`CreateFest`, `UpdateGameSettings`,
  `AssignFestNumbers`, `RevertGame` and so on). The form calls that function
  and renders, and the JSON route calls the same function and answers. So there
  is one set of checks and refusals behind both.
- **`dope-cli`** holds the server and the token (`~/.config/dope-cli`, 0600)
  and sends any request (`dope-cli api METHOD PATH [body]`). The `dope-api`
  skill lists the endpoints. Unlike xy nothing is encrypted, so the CLI needs
  no keys and stays thin.

## Consequences

- A leaked token is the whole account until it expires or the password changes.
  We accept this for the same reason xy does: scopes would need a permission
  model on every route, and nobody has asked for one.
- A new host form is not finished until its JSON twin exists. Put the work in
  a function the form and the route both call, and add the route to
  `hostpages.APIRoutes`.
- The PATCH twins (`/settings` of a fest and a game) read the current values and
  apply the body on top of them, so an agent sends only what changes. The forms
  still send everything.
