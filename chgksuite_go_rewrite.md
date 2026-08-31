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
| `textparse/` | `parser.py` (ChgkParser) | chgk only |
| `chgkimport/` | `parser.py` `parse_wrapper` | .docx/.4s/.zip |
| `docx/` | `composer/docx.py` | every `compose docx` switch, oracle-tested |
| `typstdoc/` | `composer/typst.py` | desktop/mobile, no options |
| `handout/` | `handouter/{gen,runner,split_fit,utils}.py` | .hndt→.typ→PDF + split_fit + image shrink |
| `typstwasm/` | (typst binary + `handouter/installer.py`) | done, and better |
| `imgconv/` | `composer_common.proportional_resize` + Pillow | done for export |
| `typoedit/` | — | xy-only (editor-side typography) |
| — | `composer/{pptx,telegram,lj,db,markdown,openquiz,stats}.py` | **not ported** |
| — | `parser_db.py` | **not ported** |
| — | `board.py`, `board_config.py`, `xy_crypto.py` | not needed (xy serves `trello_compat.go`) |
| `cmd/chgksuite/` | `cli.py` | `compose docx` only, so far |
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
- [x] **.docx export**: reuses chgksuite's `template.docx`; body/rels byte-parity,
      every `compose docx` switch included (spoilers, screen mode, the layout
      flags): `scripts/gen_docx_oracles.sh` writes chgksuite's own
      `word/document.xml` per fixture per switch, `options_test.go` compares.
- [x] **PDF export**: `typstdoc`, desktop and mobile page setups.
- [x] **Handouts**: `.hndt` → `.typ` byte-exact vs chgksuite, PDF via typst,
      `split_fit` row fitting (~12× faster, row counts match), the image-shrink
      refinement, per-question and all-questions PDFs, pdfcpu compression.
- [x] **typst as a library**: wasm under wazero, pooled; nothing decrypted ever
      reaches a filesystem. Replaces the whole `installer.py` dance.

## Not ported

### A. Export formats

- [ ] **A1. pptx** [both]: `composer/pptx.py` (1724 lines), the single biggest
      gap: slide layout, template cloning, service slides, font measurement and
      shrink-to-fit, inline images, answer grids, `pptx_config.toml`. Needs an
      OOXML pptx writer and a text-measurement path (the fonts are already
      embedded for handouts).
- [ ] **A2. markdown / redditmd** [both]: `composer/markdown.py` (121 lines).
      Small and self-contained; wants an image host for `(img …)` (see A6).
- [ ] **A3. base (db.chgk.info txt)** [both]: `composer/db.py` (214 lines).
      Small; same image-host dependency.
- [ ] **A4. openquiz JSON** [both]: `composer/openquiz.py` (177 lines). Small.
- [ ] **A5. lj (LiveJournal)** [cli]: `composer/lj.py` (403 lines): HTML
      rendering + the XML-RPC challenge/post flow. The HTML renderer is reusable;
      the posting half is legacy.
- [ ] **A6. Imgur upload** [cli]: `composer_common.Imgur`; what A2/A3/A4 use to
      turn a local image into a URL. In xy an attachment URL would do instead.

### B. Telegram

- [ ] **B1. Telegram exporter** [both]: `composer/telegram.py` (1959 lines):
      HTML formatting inside Telegram's length limits, message splitting, photo
      groups, spoilers, channel/discussion-chat resolution, polls
      (`poll_config.toml`), `--skip_until`, `--dry_run`. The suite already runs
      Telegram bots in-process (`tgbot`), so the transport exists; the formatting
      and packet-walking do not.
- [ ] **B2. MTProto account auth** [cli]: the interactive `api_id`/`api_hash` +
      2FA login `telegram.py` uses for user-account posting. Bot-token posting
      covers most of B1 without it.

### C. Statistics

- [ ] **C1. add_stats** [both]: `composer/stats.py` + `--rating_ids`: fetch
      tournament results from rating.chgk.info and append «Взяли: N/M» plus
      team lists to each question's comment. ~150 lines of logic + an HTTP client.
- [ ] **C2. custom csv/xlsx stats** [cli]: `--custom_csv`, `--custom_csv_args`:
      the same, from a local rating-format spreadsheet.

### D. Parsing

- [ ] **D1. СИ parser** [both]: `si_parse_docx` / `si_parse_text`; `.si4s`.
- [ ] **D2. Тройка parser** [both]: `troika_parse_docx` / `troika_parse_text`;
      `.tr4s`. (`fsource` already knows troika/brain numbering; only the
      text→structure half is missing.)
- [ ] **D3. db.chgk.info import** [both]: `parser_db.py` (445 lines): a
      tournament URL → 4s.
- [ ] **D4. `.txt` import** [xy]: `textparse` handles it, `chgkimport.Parse`
      does not accept it. Small. Encoding detection (chardet, `--encoding`) is
      part of this.
- [ ] **D5. parse knobs** [both]: `--defaultauthor`, `--download_images`,
      `--tour_numbers_as_words`, `--links`, `--fix_spans`, `--no_image_prefix`,
      `--numbers_handling none`. Each is small; none is wired today.
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
      template's Arial / Noto Sans.
- [ ] **E5. pdf options** [both]: `--nospoilers`, `--pdf_config` (typst
      typography config), `--rawtypst`. The PDF export takes none of E1–E3
      either: `typstdoc` is still device-only.
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
      smart|light` are not ported.
- [ ] **G3. `--labels_file`, the settings file, `--config`** [cli]: the CLI's
      config plumbing. (`--add_ts` is done: `compose docx` takes it.)
- [ ] **G4. A Go CLI**: `cmd/chgksuite` exists and runs `compose docx` with
      every switch of E1–E3, writing the same filename chgksuite does. Every
      other command is still Python-only; each ports as its feature does.
- [ ] **G5. wasm measurement** [xy]: `typstwasm` cannot answer a `query`, so
      F1 is inert under it. Needs a small export in `typst-wasm/` (the
      introspector's position for a label) and a wasm rebuild.

## Decisions (2026-08-31)

- **Finish line**: xy *and* a Go `chgksuite` CLI over the same packages, so the
  Python tool can be retired. The [cli]-tagged items are therefore in scope,
  except the deliberate non-goals (D6; A5's XML-RPC half and B2 are last).
- **Order**: as below. Chunk 1 (E1–E3 + F1) is done.
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
2. **A2–A4**: markdown, base, openquiz: three small exporters, and with them
   `compose` covers most of what the Python one is used for.
3. **D1–D2, D4**: СИ and тройка parsing, plus `.txt`: the `parse` command,
   which the Go CLI does not have at all yet.
4. **C1**: add_stats; self-contained and genuinely useful after a tournament.
5. **B1**: the Telegram exporter, bot-token path only.
6. **A1**: pptx, the big one, worth its own series.
7. **G1**: i18n, once there are enough label sites to be worth a table.
