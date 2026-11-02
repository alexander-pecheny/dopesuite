---
status: accepted
date: 2026-10-29
---

# A площадка of a synch fest works without a network

A площадка of a synch tournament often plays in a bar or a school where the
network is poor. A live fest needs the network anyway, since its audience
watches it as it happens, but a synch площадка only has to get its rosters and
answers to the server in the end.

## Decision

- **A service worker saves the pages a host asked for.** The площадка page has
  a button «Подготовить к работе без сети». It saves that page and its Games'
  pages (`/sw.js`, `web/ts/sw.ts`), with the scripts, styles and fonts they
  load, and a copy of the площадка's teams for the editor. A saved page is
  network-first with a four-second fallback to the copy; versioned assets are
  served from the cache; nothing else is cached, `/api` never. It works in an
  ordinary browser tab, without installing anything.
- **Team changes wait in an outbox** (`web/ts/offline.ts`, localStorage, per
  fest): a team made, changed or taken off, and a площадка's numbers, sent one
  at a time in order. A team made on the device carries a client id
  (`fest_teams.client_id`, v50), so a create sent twice makes one team, and
  the number the device chose (`venue_number`), which the server keeps or
  refuses. A refusal is shown on the page once.
- **ОД answers wait in the game page's own pending edits**, kept for a week on
  a synch fest instead of fifteen minutes, and go only after the outbox is
  empty: the server keeps a sheet's slots to the teams it knows, so an answer
  for a team it has not seen yet would lose its slot.
- **The page draws the server's copy with the outbox laid over it.** The ОД
  document names a team by its number at its площадка, so each change keeps
  the name and number it was made against. A renumbering renumbers the
  площадка's answers the way the server will, both in the answers the page
  keeps unsent and in the document it shows. A document remembers which
  changes it already shows, so a live delta built on it does not take them
  twice.
- **Logout clears the site's data** (`Clear-Site-Data`), and asks first when
  this device keeps changes the server has not seen.

Two phones of one площадка entering the same question offline end with
whichever reached the server last. One scorer per площадка is the rule the
manual states.
