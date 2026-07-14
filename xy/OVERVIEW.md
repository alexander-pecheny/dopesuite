# TLDR
We're making a Trello clone geared specifically towards ЧГК trivia quiz

# Architecture & Stack
- heavily reuse ../dope (its sibling in the dopesuite monorepo): Go+sqlite for backend, vanilla JS for frontend
- reuse Dope's design system, auth code etc – in short, reuse as much code as possibly. Might extract that into a shared module later, for now just copy
- API-first, anything that can be done using UI should be able to be done using API (except maybe search, see below). We shouldsupport some subset of Trello API to ease migration for existing clients using Trello (TBD steps for this one)
- Tightly integrated with chgksuite, can import and export from it (TBD, can be done later after we have the core functionality)
- Major differences from dope:
	- everything in the user-entered data (boards, cards, labels, comments, file attachment) is symmetrically encrypted client-side with a passphrase. When board is shared, user shares the passphrase with collaborators outside of dope. Passphrase is saved browser-side (cookies or localstorage, whichever is safer, your choice) so users don't have to reenter it each time. Client side creds theft is less of a concern here, main concern is that site operator can't read user data and if server is hacked the hacker gets nothing
	- Offline mode. We want editing ans creating boards/cards/etc. work offline on mobile via PWA and syncing later when internet reappears

# Basic features
- users log in via password or login code in Telegram
- user can create boards. Inside a board, user can create named lists and cards.  Cards have descriptions but don't have titles, they are automatically derived from description (first few words and then fade out like Dope table cells). Both lists and cards can be dragged around to reorder
- On board creation, user specifies a passphrase that is used for client side encrypting all data in the board
- Card descriptions and comments are simple plain text fields with monospace font  when editing
- Instead of just 'comments' we have a section called 'timeline' that includes both comments and any changes to the card: edit description (shows before / after in two side diff style), add/remove label, add/remove/replace file attachment.
- All attachments are encrypted as well as other fields. Image attachments are recompessed into webp quality 70 to save space, unless user explicitly checks a box to upload image losslessly
- User can create any number of labels with ahy hex color, labels are associated with a board
- User can copy/move card/list to another board he has access to, it transfers everything, re-encrypting data. The labels are created anew if such labels do not exist at the target board
- There is a special kind of list called 'test list', all cards in it are 'test cards'. These cards correspond to test sessions: gatherings where preliminary versions of questions are being played and testers share ideas how to improve them. Instead of freeform text, test cards have the following attributes:
	- title is date, time
	- description is list[int], where ints are rating.chgk.info player ids for those who has been present at this test session. Separately we maintain a correspondence of these ids to names
	- each such card automatically creates two labels: green '{yyyy-mm-dd HH:MM} взяли' and corresponding red one for 'не взяли'. User then manually assigns the labels to the questions in the session
- User can search through all boards he has access to. This is a tricky one since data is encrypted, we might need to build a indexeddb-based client side search index or smth like that