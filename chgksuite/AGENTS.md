# chgksuite — the Go port of chgksuite

This module is the Go port of chgksuite, the Python tool that turns ЧГК
question packages into documents and back: the 4s format, the .docx, .pdf and
.pptx exports, handouts, import, the Telegram, LiveJournal and other
publishers, and the `chgksuite` command line. It also holds the parts of xy
that this code needs: `listexport` (what a List exports as), `cardkind` (the
Card Kind table), and `xyenvelope` (xy's Envelope, which `board` uses to sync a
board). How far the port has got is in `../chgksuite_go_rewrite.md`, and where
it is going is in `../docs/chgksuite-v2-plan.md`.

The module path is `code.pecheny.me/pecheny/dopesuite/chgksuite`. The packages
are public so that other modules can import them. xy does that through a
`replace` in its `go.mod`, and its server builds the exports, the import and
the handouts on them. This module imports dopecore and no other module of
ours.

Anyone can install the CLI with `go install
code.pecheny.me/pecheny/dopesuite/chgksuite/cmd/chgksuite@latest`. Go refuses
that for a module whose `go.mod` has a `replace`, so this one has none: it
requires dopecore at a pushed pseudo-version. Inside the repo, `go.work` puts
`../dopecore` in its place, so a change to dopecore reaches these tests at
once. When this module starts to use such a change, push it and move the
requirement with `go get code.pecheny.me/pecheny/dopesuite/dopecore@<commit>`,
or `go install` builds against the old dopecore. Module releases are tags
named `chgksuite/vX.Y.Z`; the CalVer tags `chgksuite/2026.…` publish the
container image instead.

## What to know before you change it

- **typst.wasm is not in git.** `typstwasm` embeds it, and it is 30 MB.
  `just build-wasm` compiles `typst-wasm/` (Rust) into
  `typstwasm/typst.wasm`. Do it once per clone and again only on a typst
  bump. Every recipe that compiles typstwasm fails with that instruction until
  the file exists.
- **Two generators write into xy.** `go generate ./fsource` writes
  `../xy/web/ts/markers_gen.ts` and `go generate ./cardkind` writes
  `../xy/web/ts/cardkind_gen.ts`. `just generate-check` fails if either is
  stale. Several testdata corpora are also read by xy's jstest suite:
  `fsource/testdata/parity.json`, `listexport/testdata/cases.json`,
  `typoedit/testdata/pass_cases.json` and `xyenvelope/testdata/envelope.json`.
- **Parity tests read the Python tool.** Many tests compare the output with
  `.canon` files from a checkout of chgksuite at `~/chgksuite` (or
  `XY_CHGKSUITE_TESTS`). When they fail on lines you did not touch, pull that
  checkout first. The oracle scripts in `scripts/` regenerate the fixtures from
  the Python tool.
- **Strings** live in this module's own Catalog, `i18nstrings/` (root
  `docs/adr/0006`). The rules are the ones in the root `AGENTS.md`. Some ids
  are also in xy's Catalog, because xy's TypeScript shows the same words.
- `third_party/lingua-go` is a trimmed copy of lingua-go that `credits` uses.
  Its README says why it is trimmed. It is a package of this module rather
  than a module of its own, because a `replace` would stop `go install`.

## Recipes

```
just check           # this module: fmt + vet + tidy-check + generate-check + test
just pre-commit      # the whole repo, via the root justfile
just chgksuite       # the CLI → ~/.local/bin, typst not embedded (~14 MB)
just chgksuite-wasm  # the same with typst inside (-tags wasmtypst)
just build-wasm      # typst-wasm/ → typstwasm/typst.wasm (needs Rust; ~4 min)
```

## Layout

```
cmd/chgksuite/         the Go side of chgksuite's own CLI over the packages below: `parse`,
                       `compose docx|pdf|pptx|telegram|markdown|redditmd|base|openquiz|lj|add_stats`,
                       `handouts generate|run|split_fit|pack|create_html`, checked against the
                       Python tool's output. Renders with the typst BINARY (--typst /
                       $CHGKSUITE_TYPST / PATH): a shell user's plaintext is already on their
                       disk, and the binary starts in milliseconds. `just chgksuite` builds it
                       at 14 MB and needs no Rust and no wasm; `just chgksuite-wasm` carries
                       typst along (-tags wasmtypst, 49 MB) for a machine that has none.
                       Nothing in xy uses it; see ../chgksuite_go_rewrite.md
listexport/            what a List exports as: Assemble(cards) → the 4s (Versions folded, kind
                       markers, blank lines as (LINEBREAK)), the game (a Theme makes it SI) and
                       the .hndt (preamble + one block per question with a Handout; the label
                       must open an unbracketed one — xy's rule, not handout.Generate's). The
                       REFERENCE: xy-cli calls it, and xy/web/ts/listexport.ts is the browser's
                       copy, held to testdata/cases.json (written by `go test
                       ./listexport -update`, read by xy/jstest/listexport_parity.test.js).
                       The copy stays because a bare .4s is written offline and the board's
                       handout badge is drawn on every render
cardkind/              the Card Kinds and what each does (numbered and in which count, exported,
                       carries handouts, versioned, the marker a plain one exports with, restarts
                       the Theme count, sets the base, makes the tour SI, offered in the kind menu).
                       One table: xy's allow-list is Valid, xy's tests hold the cards.kind CHECK
                       and board.dopeui's kind menu to it, listexport and xy-cli ask it, and
                       `go generate` writes it into xy/web/ts/cardkind_gen.ts for cardkind.ts
fsource/               the "4s" format, both ways: parse.go = parse_4s (oracle-tested vs
                       chgksuite --debug), compose.go = compose_4s (structure → 4s text).
                       testdata/parity.json is the Go/TS corpus: every fixture line split
                       at its marker, (img …) references, one .hndt the browser writes and
                       Go parses; parity_test.go is the oracle (regenerate with
                       `go test ./fsource -run TestParity -update`),
                       xy/jstest/fsource_parity.test.js the other reader. markerMapping is the
                       one marker table: `go generate` writes xy/web/ts/markers_gen.ts
typo/                  typotools.py: the typography pass (quotes/dashes/stress accents/
                       %-decoding) + URL-aware underscore escaping
typoedit/              the typography pass: typo (quotes, dashes and %-decoding, which is every
                       knob except accents, as those have their own button), plus inline's gluing
                       of nbsp and nbhyphen. It is applied to the 4s SOURCE, not to the value of a
                       field. Every line is split at its marker first (fsource.SplitMarker),
                       because a pass run over raw 4s reads a list item's leading "-" as a stray
                       hyphen, turns it into an em dash, and destroys the list.
                       NOTHING IN PRODUCTION CALLS THIS. The button runs the TypeScript port in
                       xy/web/ts/typo.ts instead, because question text must not be sent to a server
                       that may not be allowed to see it. This package is the parity oracle, and
                       both test suites read testdata/pass_cases.json
docxread/              .docx → plain text — a hand-rolled python-docx (zip/OPC, runs,
                       hyperlinks, numbering, tables, image extraction, in memory, no fs)
docread/               Word 97–2003 .doc → the same plain text, minus images, list numbers
                       and formatting: OLE container, FIB, piece table. Only `chgksuite
                       credits` uses it; doctest/ writes minimal .doc files for tests
textparse/             parser.py's parsers: plain text → structure. The chgk/brain parser
                       runs in stages, one file each: label.go (each line typed by its
                       marker), starts.go (questions that carry no marker), group.go (which
                       field an unlabelled line belongs to), fields.go, pack.go. It is no
                       longer a literal port: generated_test.go renders every canon in 30
                       styles, and testdata/generated_canon.txt lists the renderings that
                       must read back into their canon (a ratchet; -update after a gain).
                       si.go + troika.go are SiParser and TroikaParser, which read the
                       document's outline through the "$$HN$$" markers docxread writes for
                       them; db.go is parser_db's PLY lexer over db.chgk.info's own text
                       export (a .txt that opens «Чемпионат:»). All byte-parity tested
                       against chgksuite's corpus canons
textenc/               a .txt package whose encoding nobody recorded: UTF-8, or whichever of
                       CP1251/KOI8-R/CP866/ISO8859-5 spells the most Russian-looking letters
                       (chgksuite asks chardet; Go has none)
pptx/                  `compose pptx`: the deck a package is played from. A hand-rolled
                       OOXML pptx writer over a template (chgksuite's, or one the config
                       names), the slide builders, service slides cloned off that template,
                       pptx_config.toml, the shrink-to-fit pass and --optimize_size.
                       Byte-parity per authored part vs chgksuite. Two things are not:
                       optimize.go re-encodes pictures with Go's encoders (same sizes, to
                       0.1% — imgconv/jpegopt.go is the Huffman pass Pillow's optimize=True
                       does), and measure.go says why text measurement is only pixel-close
markdown/              `compose markdown|redditmd`: a package as Markdown, or as the
                       Reddit dialect where the answer is a >! … !< spoiler
dbtext/                `compose base`: the plain text db.chgk.info takes as a submission —
                       the other side of textparse/db.go. #DATE is read by a parser that
                       covers the shapes packages use, not by chgksuite's dateparser
openquiz/              `compose openquiz`: open-quiz.com's JSON, one object per question
imghost/               what those three need and the .docx/.pdf ones do not: a picture as
                       a URL. Imgur, sharing chgksuite's own ~/.chgksuite/image_cache.json;
                       the server's openquiz export keeps its own cache beside the DB,
                       since ProtectHome puts that one out of reach
chgkimport/            the import entry point: .docx/.4s/.zip → 4s source + its images.
                       Byte-parity with chgksuite's `parse` on all 12 chgk .docx fixtures
handout/               .hndt → .typ (byte-exact vs chgksuite) → PDF via typst; embeds the typst template + Noto Sans.
                       typesetter.go: the Typesetter interface. The server uses the wasm one (typstwasm), so
                       nothing is written anywhere. CLITypesetter drives the typst binary and is kept ONLY as the
                       oracle the wasm path is checked against (wasm_parity_test.go: the fitted row counts must
                       match, since split_fit binary-searches them).
                       splitfit.go: `handouts split_fit` port — per-block binary-search row fit using typst's own pagination (typst query page count, not pypdf), per-question + all-q PDFs, pdfcpu compress; ~12× faster, row counts match chgksuite. The image-shrink refinement needs a Typesetter that also measures (handout.Measurer): the typst binary answers with a query, the wasm pool cannot yet, and under it an image block simply keeps its size
tg/                    `compose telegram`: a package posted to a channel, one rich message per
                       question (<p>/<details>/<footer>/<img>), the copy Telegram makes in the
                       discussion group carrying the polls, a pinned navigation post at the end.
                       The export runs its own in-process bot (dopecore/tgbot, like the login
                       bots) for the two things a token alone cannot do: hearing the person who
                       started it, and seeing a post reach the discussion group. Oracle-tested
                       call-for-call against chgksuite (scripts/gen_tg_oracle.py)
typstwasm/             typst linked in as a library, compiled to wasm32-wasip1 and run under wazero,
                       with its World (typst's filesystem abstraction) served out of memory. This
                       removed the last place where xy had to hand decrypted questions to a
                       filesystem. It keeps a pool of instances, because split_fit fits blocks in
                       parallel; fonts are parsed once and images once per generation.
                       Each probe is about 8 times faster than spawning the CLI: 1.4ms against
                       11.3ms.
                       typst.wasm is //go:embed-ed but NOT in git (30 MB): `just build-wasm` compiles
                       typst-wasm/ (Rust) into it — once per clone, then only on a typst bump. Every Go
                       recipe (build/dev/test) depends on a guard that says so if the file is missing.
docx/                  parsed structure → .docx (OOXML), reusing chgksuite's template.docx; byte-parity tested (document.xml body + rels: spacing, run boundaries, hyperlinks) vs chgksuite, for every `compose docx` switch (docx.Options: spoilers, screen mode, noanswers/noparagraph/only_question_number). xy passes Options{} for the host's copy, screen mode for the screen copy, and screen mode + spoilers=dots for the written-testing one, each with CommentFirst from the tour's Field Order; the rest of the switches are the CLI's; regenerate the oracles with scripts/gen_docx_oracles.sh.
                       (img …) images go through imgconv.ForExport like the PDF's — see below (images.go)
typstdoc/              parsed structure → .typ → PDF via typst (the same wasm pool handouts use): the docx
                       export in the other format. template.docx's page setup transcribed into the preamble
                       (A4, 1"/0.75" margins, 12pt body, 16/14pt headings, no auto-hyphenation, page number
                       bottom-left) and Word's keeps mapped onto typst blocks: keepLines → breakable: false,
                       keepNext → sticky, pageBreakBefore → #pagebreak(weak: true). Arial → Noto Sans (the
                       faces already embedded for handouts). Emitted in typst CODE mode — every piece of
                       editorial text is a typst string literal inside text("…"), so a question full of typst
                       syntax is just characters. Two things typst forced: a pagebreak may not sit inside a
                       block (a mid-question (PAGEBREAK) splits the block in two), and a line box is measured
                       cap-height→baseline by default, which makes Word's flush paragraphs overlap by a
                       descender — hence top-edge/bottom-edge + leading: 0 in the preamble. Noto Sans has no
                       `smcp`, so (sc …) small caps are synthesized (upper + 0.8em), as Word synthesizes them.
inline/                the 4s inline layer BOTH exporters share, so the .docx and the .pdf agree
                       character-for-character: markup tokenizing (bold/italic/img/screen/hyperlink…),
                       backtick stress accents, the non-breaking space/hyphen gluing, and (img …) sizing.
                       Lifted out of docx/ when typstdoc needed it — do not fork it back.
imgconv/               jpegopt.go: OptimizeJPEG rebuilds a baseline JPEG's Huffman tables
                       for the image it holds — lossless, 2-20% off, and what Go's encoder
                       lacks against Pillow's optimize=True.
                       ForExport: encode a picture for the size it is DRAWN at — downscale to 200 dpi of that
                       size (never up), JPEG q85 unless it has transparency (then PNG). Both exporters use it.
                       Re-encoding is unavoidable (neither Word nor typst reads WebP), but re-encoding a photo
                       as PNG is lossless and huge: an 800 KB JPEG attachment used to come back as a megabyte
                       of PNG — most of the exported file. Don't "simplify" this back to a plain ToPNG.
xyenvelope/            xy's Envelope outside the browser: Unlock derives the KEK with scrypt,
                       unwraps the board's data key and checks the verify token; DataKey seals
                       and opens fields and attachment bytes. Parity-tested both ways against
                       xy/web/ts/crypto.ts over testdata/envelope.json. xy-cli and board use it
i18nstrings/           this module's Catalog (ru/*.toml → generated Strings)
scripts/               the oracle scripts that regenerate testdata from the Python tool
typst-wasm/            the Rust source of typst.wasm (see typstwasm/)
third_party/lingua-go/ the trimmed language detector credits uses
```
