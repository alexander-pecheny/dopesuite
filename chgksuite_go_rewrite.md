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
| `typo/` | `typotools.py` | on/off modes done; smart/light not |
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
| — | `composer/{lj,stats}.py` | **not ported** |
| `textparse/db.go` | `parser_db.py` | done, canon-tested |
| — | `board.py`, `board_config.py`, `xy_crypto.py` | not needed (xy serves `trello_compat.go`) |
| `cmd/chgksuite/` | `cli.py` | `parse`, `compose docx`, `compose telegram` |
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
      TWO things are not byte-identical, both of them image encoders.
      `--optimize_size` re-encodes the pictures a finished deck embeds, and
      Go's JPEG encoder is not Pillow's, so the media differ (the deck comes
      out about a tenth larger). It is on by default, as in chgksuite; the
      oracles are generated with it off.
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
- [ ] **B2. `stop_if_no_stats`** [cli]: chgksuite refuses to publish a package
      whose questions carry no «Взятия:» when the setting is on. Trivial, and it
      belongs with C1.

### C. Statistics

- [ ] **C1. add_stats** [both]: `composer/stats.py` + `--rating_ids`: fetch
      tournament results from rating.chgk.info and append «Взяли: N/M» plus
      team lists to each question's comment. ~150 lines of logic + an HTTP client.
- [ ] **C2. custom csv/xlsx stats** [cli]: `--custom_csv`, `--custom_csv_args`:
      the same, from a local rating-format spreadsheet.

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
      `--preserve_formatting`, `--single_number_line_handling`, `--add_ts`, all
      checked against the Python CLI's output.
      Left out: `--download_images` (fetches remote pictures into local files;
      a convenience, and the only knob that touches the network) and
      `--fix_spans`, which belongs to the docx engines of D6. The typography
      knobs a parse also takes — `--typography_dashes|whitespace|percent`,
      `--replace_no_break_spaces`, `--replace_no_break_hyphens` — are G2 with
      the quotes and accents: the pass is one, so its switches go in together.
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
- [ ] **E5. pdf options** [both]: `--nospoilers`, `--pdf_config` (typst
      typography config), `--rawtypst`. The PDF export takes none of E1–E3
      either: `typstdoc` is still device-only. `--optimize_size` rides here: it
      recompresses the pictures a finished .docx or .pptx embedded, which Go
      does at the other end instead (`imgconv.ForExport`, 200 dpi, q85), so the
      switch has no lever to pull yet.
- [ ] **E6. merge several sources** [cli]: `compose --merge`.

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

- [ ] **G1. i18n** [both]: 10 label sets (`labels_*.toml`) and 10 regex sets
      (`regexes_*.json`). Go hardcodes Russian labels in `docx/` and `typstdoc/`
      and Russian regexes in `textparse/`.
- [ ] **G2. typography smart/light modes** [both]: `typo.Options` is
      boolean-only; `--typography_quotes smart`, `--typography_accents
      smart|light` are not ported. The СИ parser's own "a package that already
      has « » gets no quote pass" rides on this.
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
3. **A2–A4**: markdown, base, openquiz: three small exporters, and with them
   `compose` covers most of what the Python one is used for.
4. **C1**: add_stats; self-contained and genuinely useful after a tournament.
   B2 rides along with it.
5. **D3**: db.chgk.info's export format, whose oracle is already in the corpus.
6. **A1**: pptx, the big one, worth its own series.
7. **G1**: i18n, once there are enough label sites to be worth a table.
