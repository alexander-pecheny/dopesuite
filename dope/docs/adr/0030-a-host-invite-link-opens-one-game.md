---
status: accepted
date: 2026-10-16
---

# A host invite link opens one Game

A tournament played in several cities at once is one fest with a Game per
city, and each city's organizers should run their own Game and no other.
dope could already limit a host to some Games (`fest_game_hosts`), but an
admin had to add every host by nickname, and the nickname only resolves once
that person has signed in to dope at least once. Collecting nicknames from
organizers in five cities before the tournament is the slow part.

## Decision

An admin mints a link for one Game on the fest dashboard (or through
`POST …/games/<game>/invites`). Whoever opens `/join/<code>`, signs in and
presses the button becomes a host of the fest limited to that Game. A host
limited to other Games gets this one as well. Someone who already runs the
Game, meaning an admin, the creator or a host nobody limited, counts as a
member and is left as they were, so a link never narrows anyone's access.

The link's machine is xy's (`dopecore/invitelink`): the use cap, the expiry,
revoking, deleting, and the list of who came in. Its scope is the Game, not
the fest, because the package's `AddMember` knows only the scope and the
person, and a Game is exactly what the link has to name. A link goes with its
Game when the Game is deleted. dope offers no approval queue: the admin
already chose who gets the link, and a host is less than xy's board member.
Joining is a `fest:access` write like any other change to the access list.

## Consequences

- A link only ever makes a host. Admin and creator stay hand-assigned, so a
  leaked link costs at most an unwanted scorer in one Game, whom the admin
  removes on the same page.
- The registration invite codes (`invites`, `just invite`) are a different
  thing and stay as they are.
