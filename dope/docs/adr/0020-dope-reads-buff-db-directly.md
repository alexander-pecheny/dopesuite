---
status: accepted
date: 2026-09-02
---

# dope reads buff.db directly

Venues need three things from rating.chgk.info that dope must answer in
milliseconds while a Representative types: player names for the состав
suggest, the tournaments playable on a given date for a voting, and a team's
base roster for the current season to derive Б/Л flags. buff already mirrors
the rating site into `/home/ap/buff/buff.db` on the same box, nightly, in
`journal_mode=delete`, under the same unix user dope runs as.

## Decision

dope opens buff.db read-only (`DOPE_BUFF_DB`, `mode=ro`) and queries it
through one small package. Base rosters are added to buff's mirror
(`team_seasons`, filled by its Python updater and a one-off backfill) rather
than fetched live from `api.rating.chgk.net`: the mirror answers a hundred
times faster and the one-day lag only costs a player a Л flag the
Representative can see and understand.

Considered and rejected: buff growing a JSON API for dope (a second hop and an
API surface with a single client), and dope calling the rating API live with
a cache (fresh, but slow on first touch and rate-limited).

## Consequences

- The dope unit needs `ProtectHome=tmpfs` plus `BindPaths=/home/ap/buff`:
  systemd only honours a bind under `/home` when the rest of it is hidden by
  a tmpfs, not when it is inaccessible. The deploy script does not manage
  unit files, so this is a hand edit on the box, once, for `dope.service`
  and `dopetest.service` (dopetest done 2026-09-02).
- dope's schema coupling to buff lives in one package and fails soft: a
  missing file or table gives an empty suggest and Л flags, never an error
  page.
- The tournament picker follows the same rule (2026-09-05). It used to ask
  rating.chgk.info for each card's editors, forecast and requests — two
  calls per tournament, a few hundred per screen, seconds before the page
  drew. buff mirrors all three (`editors`, `difficulty_forecast`,
  `tournament_requests`), so the picker is now one query.
