# An appeals list is filled from the rating site, and the jury votes in xy

The appeal jury of a synchronous tournament used to work in Trello through
`trello_arbiter`. That script copied the tournament's appeals from
api.rating.chgk.info into a Trello list, one card per appeal with the question
text pasted in. The jury wrote «Вердикт: принять» into each card, and the
script scraped those words back into a spreadsheet that someone uploaded to the
site by hand. xy now does the same job on an ordinary board.

## The shape

- A List of Type `appeals` holds the appeals of one tournament. Its settings
  are a rating tournament id and a link to the List that holds the packet. If
  that List is in a List Group, the whole group is the packet, numbered as it
  is everywhere else. The settings live in `lists.meta`, an Envelope like the
  title.
- Each appeal is a Card of Kind `appeal`. Its content is JSON rather than 4s:
  the rating site's appeal (id, type, question number, the answer, the text),
  the controversial answers on that question with their statuses, what the site
  says about the appeal now, and the jury's verdict (a status and a text). The
  question text is not copied. The card renders the packet's question N as it
  stands, so an edit to the packet reaches the jury.
- A vote is one row per (card, member) in `card_votes`, and its value («за» or
  «против») is an Envelope. The verdict's status is the majority of the votes
  unless somebody sets it by hand, and a hand-set status always wins. Votes are
  optional: a jury that discusses in comments just sets the status.
- The board keeps one rating API token (`rt_…`) in `boards.rating_token`, an
  Envelope. The token is the jury member's own, from their page on the rating
  site, and needs the right to resolve appeals on that tournament.

## Why the server relays

The browser cannot talk to api.rating.chgk.info, because the CSP allows no
third-party origin, and xy keeps it that way. So the page asks xy-server to make
the call: `POST /api/rating/appeals` fetches the appeals and controversials, and
`POST /api/rating/verdict` posts one verdict. The token travels in the request
body, the server holds it only for the length of the call, and nothing the
rating site returns is stored on the server. The page encrypts what comes back
into Cards, the same way an import encrypts what `/api/import/parse` returns.

## Considered options

- **Votes as Timeline events.** The лента and the Bundle would carry them for
  free. But the list view needs every card's votes on load, which would mean
  reading the whole board's Timeline, and a vote is state that is replaced, not
  an entry that is appended.
- **Votes inside the card's JSON.** No new table. But two jurors voting at once
  would each overwrite the other's vote.
- **The token per user, on the server.** Simpler to look up, but it would be the
  one secret xy stores in plaintext. Kept in the board, it is encrypted under the
  board's key like everything else the jury types.

## Consequences

- Who voted is plaintext metadata, the same as who wrote a comment. How they
  voted is not.
- Votes are per-member state, so a Transfer or a Bundle carries the appeal cards
  and their verdicts but not the votes, the same way it leaves mentions behind.
- A refresh adds the appeals the list does not have yet and updates what the
  site says about the existing ones. If the text of an appeal changed on the
  site, the card takes the new text and says so. Votes and verdicts are never
  touched by a refresh.
- The export sends only the verdicts that differ from what the site has, after
  the jury ticks them in a preview.
- Not every jury can get a token that writes. For them the list also downloads
  its verdicts as an .xlsx in the layout of the rating site's own verdict table,
  which they upload on the site by hand. The file is built in the browser, so it
  works offline and the server never sees the verdicts.
