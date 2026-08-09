---
status: accepted
date: 2026-08-09
---

# A Participant's number belongs to its Game, not to the Fest registry

A Fest's registry row holds identity — name, city, players — and no playing number; the number lives on the Participant's entry in a Game and is dealt from 1 within it. Every workbook a Fest is transcribed from numbers its own entrants, and one Fest's Games rarely share an entrant list: СтудЧР-2026's ЭК numbered 48 teams while its ОД numbered 65 of the same registry. A fest-unique number cannot express that, and the attempt to work around it — one Fest per Game — split a single championship into three.

## Consequences

- `game_participants` stops being a vestigial table and becomes the entrant list: who plays this Game, under which number, in what seed order. A team knocked out before its first бой is still visibly an entrant.
- Every read of a participant's number is now scoped by Game. The Numbering guard checks a Game's entrants, not a Fest's registry.
- A Fest may still carry a registration number for its own paperwork; it is not a playing number and nothing scores against it.
