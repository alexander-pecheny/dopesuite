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

The page's input tab enters one sheet at a time: a venue's host sees their own,
and an organizer switches between the venues. The detailed tab lists every team
by name with its venue, since a fest number means nothing to anyone there.

## Consequences

A venue renumbering its teams remaps its own sheet (`VenueSheets`), and leaves
the others alone.

The Games per venue an older synch fest has stay as they were, and its
organizers delete them once the fest's OD is there.
