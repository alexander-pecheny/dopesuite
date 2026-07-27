# A transferred Test Session is a dated snapshot, not a replica

> Still current. Its "test label" wording predates
> [ADR-0004](0004-a-label-assignment-carries-an-optional-playing.md): what
> travels with a copied question is now its Playing plus any labels scoped to
> it, matched on the same `key`.

A Test Session records a real sitting, so in principle there is one true version
of it and every copy on another board should track that version. xy's crypto
forbids exactly that: boards share no key, the server must not learn that two
boards hold the same session, and so a session moved with a question is
necessarily re-encrypted into an independent row. We decided to accept the
divergence and make it legible instead of hiding it — the copy carries an
`origin: {board_name, copied_at}` in its `meta_enc`, and the Тесты panel shows
it as «копия с доски N от 3 марта».

## Considered options

A random `key` inside `meta_enc`, minted once and copied verbatim, is what stops
a second question from the same test creating a twin session on arrival. It was
tempting to read that key as identity — same key, same session — and to sync the
two. Nothing can: the server sees only ciphertext, and a client holds both keys
only in the moment of the copy. A refresh action («обновить из исходной доски»)
is possible later for the case where both boards happen to be unlocked on the
same device, but it can never be the guarantee, only a convenience.

## Consequences

- The tester list on the receiving board is frozen at transfer time. A tester
  added to the original afterwards does not appear on the copy, which matters
  because that list is what the «Видели» line reads to warn people who have
  already seen a question.
- The provenance stamp is the mitigation: a stale answer is at least a *dated*
  answer, and the reader can go look at the source board.
- `key` means "the same sitting", not "the same row". Two rows with one key are
  expected and correct.
