---
status: accepted
date: 2026-09-20
---

# dope reads buff.db directly

The screen can show each team's flag, and it drew that flag from a list of
cities written by hand in `screen-board.ts`. The list had 222 cities in it and
was missing 589 of the towns whose teams have played since 2024 — including
Базель and Берн, during Helvetia Cup 2026. Every tournament adds more, so the
list could only ever be behind.

rating.chgk.info knows where every town is, and buff already mirrors that site
into `/home/ap/buff/buff.db` on the same box, nightly, in `journal_mode=delete`,
under the same unix user dope runs as.

## Decision

dope opens buff.db read-only (`DOPE_BUFF_DB`, `mode=ro`) and queries it through
one small package, `storage/buffdb`. buff mirrors the towns and the countries
they are in; the country carries the ISO-3166 alpha-2 code a flag is drawn from,
which is the one thing the rating site does not publish, so it is written down
in buff's updater and nowhere else.

A roster imported from rating.chgk.info resolves each team's country as it is
imported and stores it on the team (`fest_teams.country`). A town buff has not
mirrored yet — one registered since its last nightly run — is asked about
directly, once, by id. The page is then served a city → ISO map for that fest's
roster, and the hand-written list stays only as the answer for a dope running
without a mirror.

Considered and rejected: re-reading all 1700 towns every night (the API has no
feed of what changed, and new towns appear a few a year); buff growing a JSON
API for dope (a second hop and an API surface with a single client); and dope
calling the rating API live per page (slow on first touch and rate-limited).

## Consequences

- The dope unit needs `ProtectHome=tmpfs` plus `BindPaths=/home/ap/buff`:
  systemd only honours a bind under `/home` when the rest of it is hidden by a
  tmpfs, not when it is inaccessible. The deploy script does not manage unit
  files, so this is a hand edit on the box, once.
- dope's schema coupling to buff lives in one package and fails soft: a missing
  file or table gives no flags, never an error page.
- A team imported before this carries no country, and is looked up by the name
  of its city instead. Re-importing its fest fills the column in.
