---
status: accepted
date: 2026-10-25
---

# A synch fest plays one OD with a sheet per venue

ADR-0031 gave each venue of a synch fest its own OD. Every venue ranked its
own teams, and nothing ranked the tournament, which is the point of a
strictly synchronous one: the organizers want one table of every team.

## Decision

A synch fest plays one OD of the whole fest (`multifest_venue_id` null). The
rating import makes it, in place of a Game per venue.

Its document keeps a sheet per venue, `venues: {"<venue id>": {entries,
completed}}`. A venue's sheet is a whole OD sheet of that venue's teams under
the venue's own numbers: its hosts enter their teams there, and complete the
questions they have played. Each team carries its venue and its number there
(`venue`, `venueNumber`) beside the fest number it is scored under.

The standings flatten the sheets first (`ODState.FlattenVenues`, and
`flattenVenues` on the page): a team took a question its venue completed with
its number entered, and a question counts once any venue completed it. Every
reader of an OD — the results, the export, the seats — goes through the
flattening, so nothing else learns about venues.

A host limited to venues writes this Game through the state PATCH alone,
marked `route.Editor.Sheets()`, and every op of the patch must lie under the
sheets of their venues. They cannot replace the document, and the other
editor routes of the Game stay closed to them.

The page has a view per venue besides the whole fest's, picked by `?venue=`
and by a row of chips. A venue's view is that venue's sheet read as an OD of
its own, its teams under its numbers, on every tab: the standings place the
venue's teams among themselves, the detailed tab goes by number, and the
roster tab lists the venue's teams (`/roster?venue=`). The whole fest's view
flattens the sheets; there a fest number means nothing to anyone, so the
detailed tab lists the teams by name. The input tab always enters one sheet:
a venue's host sees their own, and an organizer's chips move the page to
another venue's view.

The line under a team's name says what tells it from its neighbours: its venue
in a table of several venues, its city otherwise (`teamPlace`). A venue mostly
repeats its teams' city, so the table never shows both.

## Consequences

A venue renumbering its teams remaps its own sheet (`VenueSheets`), and leaves
the others alone.

The Games per venue an older synch fest has stay as they were, and its
organizers delete them once the fest's OD is there.
