# The search index is plaintext on the device

Search across boards has to read what the questions say, and the questions are
ciphertext everywhere except in a tab that has already unlocked them. Decrypting
every cached board on every keystroke is absurd, and decrypting them once per
page load still pays the whole cost for a page most visits leave immediately —
so the Search Index is stored decrypted, in its own IndexedDB store next to the
ciphertext Mirror.

This is not the concession it looks like. `crypto.ts` already caches each
board's **raw data key** in `xy-keys`; anyone holding this profile directory can
decrypt the Mirror at leisure. Plaintext beside a key that opens it adds no
exposure. The rule the rest of xy keeps — nothing decrypted touches disk — earns
its keep for *generated* artefacts (a rendered раздатка, a Cache Storage entry a
service worker would replay), which outlive the tab in places no key gates.

## Consequences

- Forgetting a board's password must purge its index rows in the same breath,
  exactly as `people.forget` drops the names that board contributed. Otherwise
  «Забыть пароль доски» would leave every question readable with no key — a
  regression on the one promise that button makes. Deleting a board, likewise.
- Only code holding a data key writes the index: the board page as it loads and
  edits, and prewarm. A remote editor's change reaches your index when you next
  open or prewarm that board, so a hit may point at text that has since moved.
  Results are pointers, not quotations, and a stale row costs one click.
