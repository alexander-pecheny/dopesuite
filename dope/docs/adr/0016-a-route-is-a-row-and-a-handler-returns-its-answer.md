---
status: accepted
date: 2026-08-19
---

# A route is a row — pattern, access, handler — and a handler returns its answer

dope had four routers written by hand — the main mux's 30 literal entries,
`/api/fest/` (130 lines), `/fest/` (the viewer pages), `/host/` (340 lines) —
each repeating the same five steps: trim the prefix, split the path, compare
segment counts and literals, check the method, resolve the fest and the game
with the same three-branch `ErrNoRows`/`err`/`≤0` block (seven copies). A
route's method set, its role and its handler were four separate facts spread
over ~30 lines. Six guards (`authorizeFestRead`, `requireFestRole`, …) wrote
their refusal to `w` and returned a bool, so authorisation and response
formatting were welded together; `festaccess.FestUserRoleFromQuery` was called
from eight hand-written sites, each re-deriving "can edit" its own way, and two
sibling functions (`isViewerSubPath`, `isHostGameSubPath`) differed by one word.
249 `http.Error` calls decided a status where the error happened; four ways to
write JSON; `scoped_api.go:1090` answered 400 for what `:583` answered 500.
The authorisation test covered six endpoints of twenty-five.

## Decision

- **One dispatcher, `web/route`.** A `Table` holds routes as data: a Go 1.22
  mux pattern (`GET /api/fest/{fest}/games/{game}/state`), an `Access`
  (`Public`, `Session`, `PublicFest`, `Read`, `Member`, `Editor`, `Manager`,
  `Creator`, each optionally `.Numbered()` for the numbering guard), and a
  `Handler func(w, r, Scope) error`. The dispatcher resolves `{fest}` and
  `{game}` (id or slug), the session and the caller's role on the fest, runs
  the guard once, and calls the handler with the resolved `Scope`. The mux
  answers 405 for a wrong method.
- **Three tables, one policy each.** `/api/fest/` and `/api/auth/` use
  `DenyAPI` (401 no session, 403 no or wrong role, 409 unnumbered, 404 a
  missing or — to an outsider — private fest); `/host/` uses the host policy
  (no session or no role → back to `/host`); `/fest/` is the viewer table. A
  handler whose fest is named in the query (the SSE endpoints, `/api/import`)
  calls `Table.Admit` with the same levels.
- **A handler returns its answer.** `route.Status` carries an HTTP status;
  `WriteError` writes it, `sql.ErrNoRows` as 404, anything else as 500. JSON
  goes through `route.JSON`/`JSONBytes`; a body is read through `DecodeJSON`.
  Handlers no longer check methods, guard roles, or spell a status.
- **The matrix is a test.** `route_test.go` runs every access level against
  every caller (no session, outsider, host, admin, creator) on a public and a
  private fest, on an in-memory SQLite. A new level or policy changes one
  table and one test.

## Consequences

- The four routers are ≈60 rows. `isViewerSubPath`/`isHostGameSubPath` are one
  `route.GamePagePath(parts, host)`. `scoped_api.go` keeps only what the
  endpoints share below HTTP.
- `http.Error` in `server/` + `web/` fell from 249 to ≈105; what remains is the
  host pages' re-rendering of a form with its error, which is a page's answer,
  not a status.
- A trailing slash no longer matches (`/api/fest/1/` was accepted; the mux is
  exact). The numbering guard's 409 and its wording live in `route`.
- `gameexport`'s `Host` seam lost `AuthorizeFestRead`/`RequireFestTableEditor`:
  the table declares the access, the exporters only export.
- The host pages (`hostpages`) keep their `(w, r, festID)` handler shapes behind
  one-line adapters in the table; they can move to `route.Handler` one at a
  time. `pages.Host.RequireSameOrigin` is now unused by the router and stays
  for the page handlers that call it.
