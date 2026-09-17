# Issue tracker: Forgejo

The issues for this repo are on code.pecheny.me, which runs Forgejo, in the repo `pecheny/dopesuite`. Use the `fj` CLI for everything: it is already authenticated against that host, and it works out which repo you mean from the git remote. If there is something `fj` cannot do, call the Forgejo API, which is Gitea-compatible, using the token in `~/.config/forgejo/token`.

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

These are used by `/wayfinder`. The **map** is a single issue, and the tickets are issues that hang off it as children.

- **Map**: a single issue labelled `wayfinder:map`, holding the Notes / Decisions-so-far / Fog body.
- **Child ticket**: an issue whose body starts with `Part of #<map>`, and which is listed in a task list in the map's body. Its label is `wayfinder:<type>`, where the type is `research`, `prototype`, `grilling` or `task`. Once somebody claims a ticket, assign it to them.
- **Blocking**: use Forgejo's own issue dependencies. Through the API, that is `POST /repos/pecheny/dopesuite/issues/<child>/dependencies` with the body `{"index": <blocker-number>}`. A ticket is unblocked once every one of its blockers is closed.
- **Frontier query**: list the map's open children with something like `fj issue search -s open -l wayfinder:task`, then drop any that still have an open blocker (`GET .../issues/<n>/dependencies`) or that already have an assignee. Take the first one in map order.
- **Claim**: `fj issue assign <n> pecheny` — the session's first write.
- **Resolve**: post the answer as a comment, close the issue, and then add a pointer to it in the map's Decisions-so-far section.
