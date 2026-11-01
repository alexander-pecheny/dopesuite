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

The export compares, fact by fact and team by team, what dope has with what
the site has. Each fact is the same on both, only in dope, only on the site,
or different. Its four facts are the site ID, the roster with each player's
flag, the зачёты and the answers.

- A fact that is the same is not sent.
- A fact only in dope is sent.
- A fact only on the site stays there, and the page offers to take it into
  dope.
- A fact that differs blocks the send until the organizer picks a side for
  it. Nothing is preselected. A hint says which side changed since dope's
  last import, from the hand edits ADR-0024 keeps apart, and a button picks
  the site's side for every conflict in one go. Picking dope's side sends
  it. Picking the site's side writes the site's version into the fest
  roster as an import of that one team, which drops that team's hand edits,
  so the next export finds the two equal.

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

The export now reads one more endpoint per run and asks the organizer
something whenever the two sides disagree. On a tournament where many
representatives typed their own rosters, that may be dozens of choices,
which the bulk button keeps to one.

A зачёт dope made by hand goes to the site only when the tournament's flags
include one with the same short name.
