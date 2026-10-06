# The export, Card Kinds and Timeline payloads each have one Go reference

The review of 6 Oct 2026 found three places where xy's browser and Go each
kept their own copy of a rule, and the copies had drifted. All three now have
a Go reference that the browser's copy is generated from or tested against.

## The export assembly

The 4s source, the tour's game and the .hndt were assembled from a List's
cards in `export.ts` and `hndt.ts`, and again in xy-cli's `source.go`. The
two disagreed on unbracketed handouts and on how a .hndt splits into blocks,
and xy-cli's handouts export produced nothing.

- `xy/internal/listexport` (`Assemble`) is the reference. The browser keeps
  its copy in `listexport.ts`, held to the same 34 cases in
  `testdata/cases.json` (`go test ./internal/listexport -update`, and
  `jstest/listexport_parity.test.js` reads it). xy-cli exports through the
  Go package and sends the .hndt with every export.
- Assembly stays in the browser even though the server already sees
  plaintext at export time. A bare .4s exports offline, and the handout badge
  and the handouts menu check run on every redraw.
- A handout written without brackets counts only when its label opens the
  question. That is xy's rule. `handout.Generate` keeps chgksuite's rule (any
  mention, with a warning), because `chgksuite handouts generate` must match
  the Python tool byte for byte.
- A .hndt splits only on a line that is exactly `---`.

## Card Kind

The same questions about a card's kind were answered with literals in about
twenty places in two languages. Adding Themes touched seven files.

- `xy/internal/cardkind` is the table: numbered (and in which count),
  exported, handouts into the .hndt, Versions, the plain export marker, what
  it does to the Theme count, the tour game it implies, and whether the kind
  menu offers it. `go generate` writes `web/ts/cardkind_gen.ts`, as it does
  for the 4s markers. Tests hold the CHECK constraint, the server's allow-list
  and the menu in `board.dopeui` to the table.
- The Tester List counts Themes. Its question-only filter dated from before
  Themes existed. A Theme is tested as a whole (ADR-0018), so it counts once.

## Timeline payloads

Every writer shaped its event's JSON and encrypted it in the same line, and
readers cast what they parsed. Only Comments had a codec.

- `web/ts/eventpayload.ts` seals and opens every payload kind, and
  `xycli/eventpayload.go` is its twin. A payload that will not decrypt or
  parse, or has the wrong shape, opens as `unreadable` and never throws.
- Stored rows are read as they are: label rows from before `label_id`, and
  the old xy-cli's sorted, HTML-escaped JSON. New writes from both sides
  produce the same bytes. No row was migrated.
- `xycli/testdata/eventpayload.json` is read by a Go test and a deno test.
  Bundles and copies to another board carry the plaintext untouched.

## Follow-ups

- xy-cli finds the images a card needs with `inline.ImageRefs`, the function
  the browser/Go inline parity test checks, rather than its own scan.
- `cardkind.ForListType` gives the kind a new card gets in a List, so
  `xy-cli card add` makes what the board's add-card button makes.
- Copying a card to another board, or applying a bundle, points each comment
  image at the copied attachment, or drops it if that attachment was not
  copied.
