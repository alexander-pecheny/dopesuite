# An invite link grants membership, never the key

## Status

Accepted, 2026-08-29.

## Context

Sharing a board meant typing a collaborator's username, which the owner had to
know. The ask was Telegram's invite links: a URL you paste into a chat, capped
by uses or by time, optionally holding the joiner at the door until the owner
approves.

xy is end-to-end encrypted, and that makes "invite" ambiguous. Membership
(`board_members`) authorizes the *API* — it hands out ciphertext plus the board
name, which is xy's one deliberate plaintext. Reading the board needs the data
key, wrapped under a passphrase-derived KEK the server never sees. Today the
passphrase travels between people, out of band.

So a link could carry one, both, or neither, and each is a different product.

## Decision

**A link grants membership only.** Following it adds you to `board_members` as
an editor. You then land on the passphrase overlay knowing the board's name and
nothing else, and the passphrase still has to reach you from a person.

Everything else follows from that:

- **The code is stored in plaintext** (`board_invites.code`), unlike
  `api_tokens.token_hash`. The owner's list has to hand the link back days after
  minting — that is what managing links means — and a hash cannot. The code is a
  bearer credential for membership, not for content: anyone who can read it out
  of the database already holds every board's ciphertext and every board name.
- **Only a use that reached `joined` spends the cap.** A pending request
  reserves nothing, so a one-seat link may gather a queue and the owner picks;
  a decline refunds nothing because nothing was taken.
- **A decline is final for that link.** The `board_invite_uses` row stays as
  `declined`, and `unique(invite_id, user_id)` is what stops the declined person
  re-queueing. Another link still lets the owner change their mind.
- **Revoking keeps the row and its history; deleting removes both.** Who came in
  through a link outlives the link. Deleting drops the record, never the member:
  a link is how someone arrived, not what keeps them in.
- **Owner-only**, exactly like adding a member by username.
- **`?next=` on /login accepts one shape**, `/join/<code>` with an alphanumeric
  code. `next` is attacker-controlled, and an allow-list of one pattern is the
  whole defence against turning /login into an open redirect.

## Consequences

A stranger who follows a link is a member of a board they cannot read until
somebody tells them the passphrase. That is the intended shape: the link
automates the half that is bookkeeping, and leaves the half that is trust to
people. It also means a link leaked out of a group chat costs a stranger's
ciphertext, not the questions.

## Alternatives considered

**The passphrase in the URL fragment.** One click and you are in, and the
fragment never reaches the server. But the joiner learns the passphrase and can
reshare it past any revocation, it lands in messenger history, and a passphrase
change silently breaks every outstanding link.

**A data key wrapped to the link.** Mint a random link secret, seal the DK under
it (`rewrapKey` already re-wraps the same DK for a passphrase change), store the
blob on the invite row and put the secret in the fragment. Revocation would then
really revoke, and approval would be cryptographic rather than an ACL check —
but it makes a link a key, which is the thing this ADR declines to do.

**Membership plus a hashed code.** Matches `api_tokens` and survives a database
read, at the cost of a management list that can show a link's statistics and
never the link.
