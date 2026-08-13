# 9. Mentions are plaintext event metadata

Date: 2026-08-12

## Status

Accepted

## Context

An @-mention in a card comment must produce a stronger unread signal (red dot)
than an ordinary comment (blue dot). The whole unread machinery is
server-computed: `card_reads` watermarks per (user, card), the board snapshot's
`unread` map, the board-list rollup — none of it needs a data key, so dots work
on locked boards and cost nothing to render. But comment text is end-to-end
encrypted, so the server cannot see a mention in it.

Two options: the comment mutation declares the mentioned user ids in plaintext
event metadata, or mentions stay inside the ciphertext and every client detects
them after decrypting. The encrypted route keeps the crypto story pure but
breaks the signal exactly where it matters: a locked board can show no red dot,
and the board list would have to fetch and decrypt every unread comment on
every board to badge it.

## Decision

The client parses mentions at compose time and sends the mentioned user ids as
plaintext columns on the timeline event, alongside `author_user_id`,
`reply_to_id` and `is_excerpt`, which already leak the same kind of structural
metadata by accepted precedent. The server computes mention-unread exactly as
it computes comment-unread.

## Consequences

The server (and a DB-level attacker) learns who mentioned whom and when — not
what was said. This widens the accepted metadata surface and is irreversible
for past events. Mention ids can drift from the encrypted text (the text is
the client's rendering concern; the ids are the routing truth). Everything
else stays cheap: no push, no decryption on the badge path, red dots on
locked boards.
