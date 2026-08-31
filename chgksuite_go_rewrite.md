# chgksuite → Go: what is ported and what is not

The Go port lives in `xy/internal/chgk/` (~9.5k lines). The reference is
`~/chgksuite` (`chgksuite/`, ~13k lines of source). This file is the map: what
already matches, what does not, and in what order the rest is worth doing.

Tags: **[xy]** the xy server would use it in-app · **[cli]** only meaningful for
the standalone tool's workflow (a shell, a filesystem, an interactive account)
· **[both]**.

## Package map

| Go | Python | State |
|---|---|---|
| `fsource/` | `composer/chgksuite_parser.py` (parse_4s), `common.compose_4s` | done, oracle-tested |
| `typo/` | `typotools.py` | done, every mode |
| `i18n/` | `resources/labels_*.toml`, `regexes_*.json` | done, all ten languages |
| `inline/` | `composer/composer_common.py` (`_parse_4s_elem`, `parseimg`, nbsp) | done |
| `docxread/` | `parsing_engine.py` (python_docx engine) | done (that engine only) |
| `textparse/` | `parser.py` (ChgkParser, SiParser, TroikaParser) | chgk, si, troika |
| `textenc/` | chardet + `read_text_file` | guesses among four Russian encodings |
| `chgkimport/` | `parser.py` `parse_wrapper` | .docx/.4s/.zip |
| `docx/` | `composer/docx.py` | every `compose docx` switch, oracle-tested |
| `typstdoc/` | `composer/typst.py` | desktop/mobile, no options |
| `handout/` | `handouter/{gen,runner,split_fit,utils}.py` | .hndt→.typ→PDF + split_fit + image shrink |
| `typstwasm/` | (typst binary + `handouter/installer.py`) | done, and better |
| `imgconv/` | `composer_common.proportional_resize` + Pillow | done for export |
| `typoedit/` | — | xy-only (editor-side typography) |
| `tg/` | `composer/telegram.py` | rich messages, oracle-tested; the dead plain path is not ported |
| `markdown/`, `dbtext/`, `openquiz/` | `composer/{markdown,db,openquiz}.py` | done, oracle-tested |
| `imghost/` | `composer_common.Imgur` | done, cache shared with chgksuite |
| `pptx/` | `composer/pptx.py` | done, oracle-tested |
| `stats/` | `composer/stats.py` + the results readers of `common.py` | done, oracle-tested |
| — | `composer/lj.py` | **not ported** |
| `textparse/db.go` | `parser_db.py` | done, canon-tested |
| — | `board.py`, `board_config.py`, `xy_crypto.py` | not needed (xy serves `trello_compat.go`) |
| `cmd/chgksuite/` | `cli.py` | `parse`, `compose docx|pptx|telegram|markdown|redditmd|base|openquiz|add_stats` |
| — | `chgksuite_qt/`, `chgksuite_tk/` | not needed (the GUIs stay in Python) |

## Done

- [x] **4s parse**: `fsource.Parse`, oracle-tested against `chgksuite --debug`
      on the fixture corpus (`fsource/testdata/*.dbg.json`); one marker table
      shared with the TS reader via `go generate` (`web/ts/markers_gen.ts`,
      `jstest/fsource_parity.test.js`).
- [x] **4s compose**: `fsource.Compose`, `numbers_handling` `default` and `all`.
- [x] **Typography pass**: quotes, dashes, stress accents, %-decoding,
      URL-aware underscore escaping, non-breaking spaces/hyphens.
- [x] **Inline directives**: `(img …)`, sizes, hidden comments, square-bracket
      handout extraction, backtick stress.
- [x] **.docx reading**: OOXML → text with the `python_docx` engine's quirks:
      formatting markers, list prefixes, hyperlinks, image extraction + prefixing.
- [x] **Plain-text → structure**: `textparse`, a literal port of `ChgkParser`,
      chgk game only.
- [x] **Import**: `.docx` / `.4s` / `.zip` → 4s + images; byte-parity with
      `chgksuite parse` on all 12 chgk .docx fixtures.
- [x] **.docx export**: reuses chgksuite's `template.docx`; body/rels byte-parity
      for every switch that shapes the body — spoilers, screen mode, the layout
      flags, `--smaller_source_and_author`. `scripts/gen_docx_oracles.sh` writes
      chgksuite's own `word/document.xml` per fixture per switch,
      `options_test.go` compares. Two switches are elsewhere on this list:
      `--docx_template` and `--font` are E4, `--optimize_size` is E5.
- [x] **PDF export**: `typstdoc`, desktop and mobile page setups.
- [x] **Handouts**: `.hndt` → `.typ` byte-exact vs chgksuite, PDF via typst,
      `split_fit` row fitting (~12× faster, row counts match), the image-shrink
      refinement, per-question and all-questions PDFs, pdfcpu compression.
- [x] **typst as a library**: wasm under wazero, pooled; nothing decrypted ever
      reaches a filesystem. Replaces the whole `installer.py` dance.

## Not ported

### A. Export formats

- [x] **A1. pptx**: `internal/chgk/pptx`. An OOXML pptx writer (the package,
      its slides, its rels and content types, written as python-pptx writes
      them — part names and ids assigned once and never reissued, every .rels
      sorted by number), the slide builders (title, tour, question, handout,
      picture, plug, answer grid, blitz), service slides and numbered tour
      stubs cloned off a template, `pptx_config.toml`, the shrink-to-fit pass,
      and `--optimize_size`.
      Byte-parity against chgksuite, every authored part of every deck: six
      fixtures under `scripts/gen_pptx_oracles.py`, and — off the repo — three
      real packages of Alexander Shorin's against three real configs, 2077
      parts compared.
      TWO things are not byte-identical. `--optimize_size` re-encodes the
      pictures a finished deck embeds, and Go's encoders are not Pillow's, so
      the media bytes differ — but not the sizes: `imgconv.OptimizeJPEG` is the
      second pass Pillow gets from `optimize=True`, rebuilding a JPEG's Huffman
      tables for the image it actually holds (lossless, and the whole of what
      used to make a deck a tenth larger), and PNGs are written at Go's best
      compression, which beats Pillow's. Three real decks come out 0.1%
      SMALLER than chgksuite's. It is on by default, as there; the oracles are
      generated with it off.
      And measurement. chgksuite measures text with Pillow, which shapes
      through HarfBuzz where libraqm is installed and falls back to whole-pixel
      advances where it is not — so chgksuite's own layout is not reproducible
      across machines. This port matches Pillow's advances exactly (unhinted,
      1/64 px) and its line height exactly (read off hhea and rounded once,
      which is what FreeType does and what x/image does not), and differs only
      by HarfBuzz's GPOS kerning: under 1.4 px across a line. It showed up
      twice in those 2077 parts — an inline picture's offset, which the parity
      test allows a pixel, and one answer slide that shrank to 29pt where
      chgksuite stopped at 30.
- [x] **A2. markdown / redditmd**: `internal/chgk/markdown`. Both dialects,
      character-for-character against chgksuite (`scripts/gen_export_oracles.py`).
- [x] **A3. base (db.chgk.info txt)**: `internal/chgk/dbtext`, the other side of
      D3: consecutive «Инфо» merged, a warm-up hoisted to the top of its tour
      under a «Нулевой вопрос N» heading, a незачёт folded onto the зачёт.
      Byte-parity against chgksuite. One thing is NOT reproduced: chgksuite
      reads `#DATE` with `dateparser`, which guesses far more freely than a Go
      port sensibly can. `dbtext.parseDate` reads the shapes packages actually
      use — `dd-Mon-yyyy` (day 00 = the month's last), ISO, and `3 января 2014`
      with the Russian month names — and falls back, as chgksuite does for what
      it cannot read, to 2010-01-01. dateparser's looser guesses differ: a bare
      year becomes today's month and day, and `12.01.2020` is read month-first.
- [x] **A4. openquiz JSON**: `internal/chgk/openquiz`. Byte-parity against
      chgksuite, key order and `indent=2` included.
- [ ] **A5. lj (LiveJournal)** [cli]: `composer/lj.py` (403 lines): HTML
      rendering + the XML-RPC challenge/post flow. The HTML renderer is reusable;
      the posting half is legacy.
- [x] **A6. Imgur upload**: `internal/chgk/imghost`, behind an `imghost.Host`
      the three exporters take, so a test hands them a stub and the parity
      oracles need no network. The cache is chgksuite's own
      `~/.chgksuite/image_cache.json`, same key (sha256 of the base64) and same
      file, so the two tools do not upload each other's pictures twice.

### B. Telegram

- [x] **B1. Telegram exporter**: `internal/chgk/tg`. A package posted question by
      question to a channel, each post's copy in the discussion group carrying
      the polls, and a pinned navigation post at the end. Oracle-tested:
      `scripts/gen_tg_oracle.py` runs chgksuite's own exporter with every call to
      Telegram stubbed and records what it would send; `export_test.go` replays
      the same fixtures and switches and compares call for call.
      Only the rich rendering is ported, because it is the only one that runs:
      chgksuite sets `rich_mode` unconditionally, so its older path (plain HTML
      split into 4096-character messages, photos posted separately, `<tg-spoiler>`
      instead of `<details>`) is unreachable. If the flag ever comes back, that
      path — `make_chunk`, `assemble`, `swrap`, the length-tiered
      `tg_format_question` — is what would need porting.
      The bot runs in this process, the way xy's and dope's login bots do, rather
      than as chgksuite's second Python process passing updates through a sqlite
      file. `--tgaccount` is gone with it: the token comes from `--token` or
      `$CHGKSUITE_TG_TOKEN`.
- [x] **B2. `stop_if_no_stats`**: `compose telegram --stop_if_no_stats` refuses
      to publish a package whose questions carry no «Взятия:». A switch rather
      than a settings-file key, until G3 brings the settings file.

### C. Statistics

- [x] **C1. add_stats**: `internal/chgk/stats`. `compose add_stats
      --rating_ids` fetches a tournament's results from rating.chgk.info and
      appends «Взятия: N/M (P%)» to each question's comment, naming the teams
      when few enough took it; `--question_range` and `--team_naming_threshold`
      included. Python's round() is half-to-even and this is too, so a question
      exactly an eighth of the field took rounds the same way.
- [x] **C2. custom csv/xlsx stats**: `--custom_csv`, `--custom_csv_args`. Both
      layouts of a вопросная таблица (per team, per team per tour) from either
      format. The xlsx side reads SpreadsheetML directly rather than adding a
      spreadsheet library to the module — a results table is a grid of numbers
      and names on one sheet. Parity: chgksuite's own four real fixtures give
      the same masks, and six `compose add_stats` runs over a package are
      byte-identical to chgksuite's (`scripts/gen_stats_oracle.py`).

### D. Parsing

- [x] **D1. СИ parser**: `textparse.ParseSI`, and the `$$HN$$` heading markers
      docxread now writes for it. Byte-parity with chgksuite on both unencrypted
      СИ packages in its corpus.
- [x] **D2. Тройка parser**: `textparse.ParseTroika`, including the
      «Мультифора» variant and the source lists numbered exactly like the
      questions. Byte-parity on the corpus's троика package, plus the six
      awkward cases chgksuite keeps as literals in its own tests
      (`scripts/gen_si_cases.py` extracts them).
- [x] **D3. db.chgk.info import**: `textparse.ParseDB`, chgksuite's PLY lexer
      of fifteen exclusive states written out rule for rule, in the order PLY
      tries them. `chgksuite parse` reads a .txt that opens «Чемпионат:» as one
      and fetches the pictures and audio it names. Byte-parity on chgksuite's
      own `balt09-1.txt` canon, plus a synthetic package for what that file
      never reaches — a `<раздатка>`, a blitz, numbered comments and sources, a
      second Ответ, an `(aud …)` — diffed against the Python CLI's output.
- [x] **D4. `.txt`**: `chgksuite parse` reads one, guessing the encoding
      (`textenc`) the way chardet does for chgksuite — a file that decodes as
      UTF-8 is UTF-8, and otherwise the winner is the single-byte encoding whose
      letters are distributed most like Russian. xy's own importer still takes
      .docx/.4s/.zip only: that is a wiring decision, not a missing port.
- [x] **D5. parse knobs**: `--defaultauthor`, `--tour_numbers_as_words`,
      `--links`, `--no_image_prefix`, `--numbers_handling none`, `--encoding`,
      `--preserve_formatting`, `--single_number_line_handling`, `--add_ts`,
      `--typography_*` (G2), all checked against the Python CLI's output.
      Left out: `--download_images` (fetches remote pictures into local files;
      a convenience, and the only knob that touches the network) and
      `--fix_spans`, which belongs to the docx engines of D6. The typography
      `--replace_no_break_spaces` and `--replace_no_break_hyphens`, which the
      parse subparser declares and nothing there reads — they belong to
      compose, and go in with E5.
- [ ] **D6. alternative docx engines** [cli]: `pypandoc`, `mammoth`. Deliberate
      non-goal: they exist because `python_docx` was once insufficient, and they
      need external binaries.

### E. Export options

- [x] **E1. docx spoilers**: `--spoilers off|whiten|pagebreak|dots`.
- [x] **E2. docx screen mode**: `--screen_mode off|replace_all|add_versions|
      add_versions_columns`, table and all.
- [x] **E3. docx layout switches**: `--noanswers`, `--noparagraph`,
      `--only_question_number`, `--randomize` (`fsource.Randomize`).
      Deliberately not ported: `--no_line_break` and `--one_line_break`, which
      chgksuite declares and never reads; and `--ignore_missing_images`, since
      the Go exporters always print a marker rather than failing.
- [ ] **E4. font substitution** [both]: `--font`: `docx.py` reads system font
      tables to pick faces and rewrites the template's font. Go hardcodes the
      template's Arial / Noto Sans. `--docx_template` belongs here too: the Go
      exporter embeds the template rather than reading one off disk, which is
      what font substitution would have to change anyway.
- [x] **E5. pdf options**: `compose pdf`, with `--device`, `--pdf_config`
      (`typstdoc.Config`, the same TOML keys), `--font`, `--language`,
      `--rawtypst` and `--merge`. typst runs in this process, as wasm, rather
      than as the binary chgksuite downloads on first use. Parity is on the
      typst source, which is what either tool actually writes: fifteen runs over
      nine fixtures — both devices, a config that changes every key, both
      no-break switches — are byte-identical to chgksuite's.
      `--replace_no_break_spaces` and `--replace_no_break_hyphens` go in here
      too, on `compose` as there; `pptx`, `openquiz` and `pdf` read them and
      `docx` declares and ignores them, which is what chgksuite does (its docx
      export calls the pass through a wrapper that takes the defaults).
      `--optimize_size` now also works on docx (`imgconv.Optimize`, shared with
      the pptx pass). It has less to do there than in chgksuite: the Go exporter
      already re-encodes for the size Word draws at (`imgconv.ForExport`, 200
      dpi, q85), so the same package comes out 20 KB against chgksuite's 450 KB
      with or without the switch.
      `--nospoilers` is not a pdf switch at all — `typst.py` never reads it. It
      belongs to `telegram` (ported) and to `lj` (A5).
- [x] **E6. merge several sources**: `--merge` on every `compose` that takes
      files, naming the output after the inputs' common prefix and each one's
      tail, as make_merged_filename does. Byte-identical to chgksuite's.

### F. Handouts

- [x] **F1. image-shrink refinement**: `fit_rows_and_resize` /
      `probe_bottom_space` / `max_resize_for_rows`, on a `handout.Measurer`
      (the typst binary answers it with a `query`; the wasm pool cannot yet, and
      simply doesn't shrink). `resize_test.go` reproduces chgksuite's
      "1 -> 0.97, rows 4 -> 5" on the same handout.
- [ ] **F2. `handouts generate`** [both]: 4s → `.hndt`. Ported to TypeScript
      (`web/ts/hndt.ts`), not to Go; a Go-side import path would need it.
- [ ] **F3. `handouts pack`** [cli]: pack several split-fitted single-handout
      PDFs onto shared sheets (`pack.py`).
- [ ] **F4. `create_html` / `html2img`** [cli]: the HTML-authored handout escape
      hatch; `html2img` drives a headless browser.
- [ ] **F5. image rotation** [both]: `rotate` block key: parsed by Go, ignored
      (`handout.go:320`).
- [ ] **F6. watch mode** [cli]: `handouts run` re-renders on file change.

### G. Cross-cutting

- [x] **G1. i18n**: `internal/chgk/i18n` embeds chgksuite's own ten
      `labels_*.toml` and ten `regexes_*.json` verbatim, so neither tool can
      drift on a label or a marker. `--language` reaches `parse` (the field
      markers, and English's "leave the quotes alone" rule) and every `compose`
      that prints a label — docx, pdf, pptx, telegram — plus the handouts'
      two captions.
      One translation is needed and only one: Python's `\s` is Unicode-aware
      and matches the NBSP a .docx is full of, Go's is ASCII-only, so `i18n.ForRE2`
      rewrites it. The Russian patterns it produces are what the parser was
      using hand-written, character for character.
      Parity in all ten languages: the pdf typst source, and (with the
      export's own known sz/szCs deviation normalised out) the .docx body;
      the pptx deck in seven, every authored part; a Telegram package in
      English, call for call.
      Not ported, because chgksuite does not either: `markdown` and `base`
      print their Russian headings whatever the language, and «авторка» and
      «Нулевой вопрос» are literals in `parser.py` too.
- [x] **G2. typography smart/light modes**: `typo.Mode` — off/on/smart for
      quotes, and off/on/light/smart for accents — with `Options.Resolve`, the
      decision every parser makes once per package: text that already types its
      own « » or its own stress marks keeps them. The smart modes' second half
      is the bad-quote repair, reproduced including chgksuite's own slip (the
      Latin-single-quote loop asks a Cyrillic regex whether to go round again,
      so only the first pair is fixed). `parse` takes all five switches;
      seventeen runs over two packages match the Python CLI byte for byte.
      `--typography_percent off` still decodes, because chgksuite tests that
      switch for truth rather than for "on".
- [ ] **G3. `--labels_file`, the settings file, `--config`** [cli]: the CLI's
      config plumbing. (`--add_ts` is done: `compose docx` takes it.)
- [ ] **G4. A Go CLI**: `cmd/chgksuite` runs `parse` (chgk, brain, СИ, троика,
      .docx and .txt, with the knobs of D5), `compose docx` with every switch of
      E1–E3, and `compose telegram`. Every other command is still Python-only;
      each ports as its feature does.
- [ ] **G5. wasm measurement** [xy]: `typstwasm` cannot answer a `query`, so
      F1 is inert under it. Needs a small export in `typst-wasm/` (the
      introspector's position for a label) and a wasm rebuild.

## Decisions (2026-08-31)

- **Finish line**: xy *and* a Go `chgksuite` CLI over the same packages, so the
  Python tool can be retired. The [cli]-tagged items are therefore in scope,
  except the deliberate non-goals (D6; A5's XML-RPC half is last).
- **Order**: as below. Chunk 1 (E1–E3 + F1), B1 and D1/D2/D4/D5 are done.
- **No xy wiring.** Features land in `internal/chgk/*` and in a Go
  `chgksuite` CLI, and are checked against the Python CLI's output. What xy
  then exposes in its export modal is a separate decision, later.

## Parity notes

- **Fonts decide the fit.** xy's bundled Noto Sans carries a transplanted pause
  glyph and lays a row out a hair differently from chgksuite's copy, which is
  enough to change a row count at the margin. Handout parity is checked with
  chgksuite's fonts (`XY_CHGKSUITE_FONTS`); with xy's, the same handout may fit
  one row fewer.
- Two things chgksuite gets from python-docx were missing and now match: an
  external hyperlink relationship is reused across questions, and an image part
  is keyed by content — a question printed twice embeds one picture.

## Suggested order

1. ~~**E1–E3 + F1**~~ — done 31 Aug 2026, with the Go CLI's `compose docx`.
2. ~~**D1–D2, D4–D5**~~: the `parse` command, done 31 Aug 2026.
3. ~~**A2–A4**~~: markdown, base, openquiz — done 31 Aug 2026.
4. ~~**C1**~~, ~~**D3**~~, ~~**A1**~~ — done 31 Aug 2026.
5. ~~**G1**~~ — done 31 Aug 2026.
