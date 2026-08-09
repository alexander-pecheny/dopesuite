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

## Where it stands

A Game is created with its entrants spelled out, they are numbered from 1 inside it, and the list is written from the seating rather than from the request — so it can never claim somebody the Structure did not seat. The host picks them on the creation page; ticking nothing seats the whole registry, which is what every Game did before Games could differ, and ticking the wrong kind (a team in an individual format) is refused by name.

The Numbering guard asks the Game (`numbering.GameHasUnnumbered`). A Fest may register a team that plays nothing — СтудЧР registered 65 and its ЭК seated 48 — and an unnumbered row a Game never seats says nothing about whether that Game can be scored. A Game without an entrant list of its own still falls back to the registry.

`TestStudchrGamesShareOneFest` holds one Fest with the ЭК's 48 and the брейн's different 48, and `TestStudchrWholeFest` builds the whole championship on one Fest.

What remains is display: the roster and export surfaces still read `participants.number` for a Fest-wide number. Nothing scores against it, so the games are correct either way — but two numbers for one team is one too many, and the Fest's should become the registration number the ADR describes.
