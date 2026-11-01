---
status: proposed
date: 2026-11-01
---

# The export reconciles the fest with the rating site

ADR-0034 made the export a push. dope built the plan from its own roster
and sent it, and the site only answered where each team would go. At
Нулевой километр (tournament 13980, 1 November 2026) that cost three things.
Representatives had entered their rosters on the site after dope's import,
and the export replaced them with dope's outdated ones. The only warning was
a sentence in the page's lead text. The зачёты did not go at all, which
ADR-0034 had accepted, but the page never said so, and the organizer moved
them by hand. A team that dope made without a site ID could get one only
in the export's own «Записать ID» step, because the team editor offers the
ID only to a new team.

Five fixes between 27 October and 1 November each patched one case of the
same mistake. The export treated the site as a place to write to, while
representatives treat it as a place to type into. Two sources of the same
facts need a comparison, not a push.

## What the site offers

The site's OpenAPI description (`/docs?format=openapi`) and a read of
tournament 13980 settle what dope can see and send.

| Fact | Read | Write |
|------|------|-------|
| Roster, with Б/Л/К and captain | `GET /tournaments/{id}/results?includeTeamMembers=1`, public, every team | `results_entry/rosters`, replaces the whole roster |
| Team's зачёты | the same read with `includeTeamFlags=1`, and `GET results_entry` | `results_entry/flags`, replaces all of a team's flags |
| Answers | `GET results_entry` (`mask`, with the token; the public read hides it until `dateEnd`) | `results_entry`, replaces the whole mask |
| Request | `GET results_entry` (`request`) | `results_entry/move` |
| The flags a tournament may use | `GET /tournament_team_flags` | — |

Every write replaces a team's whole fact. A roster sent without `captain`
also drops the captain the site had, which the current export does without
saying so.

## Decision

The export compares three versions of each fact of each team: dope's now,
the site's now, and the base, which is the last version both sides agreed
on. Its four facts are the site ID, the roster with each player's flag, the
зачёты and the answers.

dope keeps the base in a table of its own, a row per team and fact. An
import writes the site's version there, and so does a send, for what it
sent. Taking the site's version also writes it. The base cannot be derived
from the hand edits that ADR-0024 keeps apart: after a send those edits are
still in dope while the site already has them, so a base derived from them
would make every later edit on the site look like a change on both sides.

| dope vs base | site vs base | The export |
|---|---|---|
| same | same | sends nothing |
| same | changed | takes the site's version into dope |
| changed | same | sends dope's version |
| changed | changed, now equal | sends nothing, and moves the base |
| changed | changed, now different | asks |

A fact with no base (a team dope never imported or sent, or answers before
the first send) counts as changed on both sides when both have it, so a
difference there is asked about too.

Only the last row blocks the send, until the organizer picks a side for
each such fact. Nothing is preselected, and one button picks the site's side
for every question at once. Picking the site's side writes the site's
version into the fest roster as an import of that one team, which drops
that team's hand edits for that fact.

The page lists every fact it settled by itself, with the side it took and
why, so a wrong edit on the site that won by the second row is visible
before the send.

The page lists everything it will not send, each with its reason: a team
without a site ID, a площадка without an accepted request, a зачёт the site
has no flag for.

A captain the site marks keeps the mark when dope sends a roster that still
holds that player.

The site ID becomes an ordinary field of every team in the team editor,
linked or unlinked by the editor's search. A team without an ID is one more
difference on the export's page, resolved by the same search, and the
export's own namesake search (`identify.go`) goes.

dope talks to the site through one package that both the import and the
export use. It reads the public results, «ввод» and the requests, and writes
through «ввод». The two packages' separate clients and response types merge
into it.

The export's tests serve recorded answers of the real site, cut down to a
few teams, instead of answers written from the code's own assumptions.

## Consequences

An export after a correct import of an unchanged tournament sends nothing,
and says so. An export can no longer overwrite what someone typed on the
site without a click that says it should.

At Нулевой километр the export would have asked nothing: the rosters came
from the import, nobody edited them in dope, and the representatives'
rosters on the site would have stayed and come back into dope.

The export reads one more endpoint per run. A wrong edit on the site made
after the last agreement wins without a question; the page names it, but
only a reader of the page will catch it. A roster edited for one Game
(ADR-0022) is outside the fest roster, and the export does not see it.

A зачёт dope made by hand goes to the site only when the tournament's flags
include one with the same short name.
