# Issue tracker: Forgejo

Issues for this repo live on code.pecheny.me (Forgejo), repo `pecheny/dopesuite`. Use the `fj` CLI for all operations — it is authenticated against the host and infers the repo from the git remote. For anything `fj` can't do, call the Forgejo API (Gitea-compatible) with the token from `~/.config/forgejo/token`.

## Conventions

- **Create an issue**: `fj issue create "<title>" --body "..."` (or `--body-file` for long bodies).
- **Read an issue**: `fj issue view <n>` for the body, `fj issue view <n> comments` for the discussion.
- **List issues**: `fj issue search -s open` with `-l <label>`, `-a <assignee>`, or a query string as needed.
- **Comment on an issue**: `fj issue comment <n> "..."`
- **Apply / remove labels**: `fj issue edit <n> labels` (see `--help` for add/remove flags).
- **Close**: `fj issue close <n>`.
- **API fallback**: `curl -s -H "Authorization: token $(cat ~/.config/forgejo/token)" https://code.pecheny.me/api/v1/repos/pecheny/dopesuite/<endpoint>`.

## Pull requests as a triage surface

**PRs as a request surface: no.** _(Set to `yes` if this repo treats external PRs as feature requests; `/triage` reads this flag.)_

## When a skill says "publish to the issue tracker"

Create a Forgejo issue with `fj issue create`.

## When a skill says "fetch the relevant ticket"

Run `fj issue view <n>` and `fj issue view <n> comments`.

## Wayfinding operations

Used by `/wayfinder`. The **map** is a single issue with child issues as tickets.

- **Map**: a single issue labelled `wayfinder:map`, holding the Notes / Decisions-so-far / Fog body.
- **Child ticket**: an issue with `Part of #<map>` at the top of its body, listed in a task list in the map body. Labels: `wayfinder:<type>` (`research`/`prototype`/`grilling`/`task`). Once claimed, assign the ticket to the driving dev.
- **Blocking**: Forgejo's native issue dependencies — `POST /repos/pecheny/dopesuite/issues/<child>/dependencies` with body `{"index": <blocker-number>}` via the API fallback. A ticket is unblocked when every blocker is closed.
- **Frontier query**: list the map's open children (`fj issue search -s open -l wayfinder:task` etc.), drop any with an open blocker (`GET .../issues/<n>/dependencies`) or an assignee; first in map order wins.
- **Claim**: `fj issue assign <n> pecheny` — the session's first write.
- **Resolve**: comment the answer, close the issue, then append a context pointer to the map's Decisions-so-far.
