---
status: accepted
date: 2026-09-03
---

# User-facing strings live in TOML catalogs and are generated into typed code

Both apps had their copy written inline: roughly 4,000 lines of Russian spread
across Go handlers, dope's Go-built pages, TS modules, `.dopeui` attributes and
export headers. On top of that, dope's `http.Error(w, err.Error())` was showing
internal errors straight to the browser. Text written at the call site is text
nobody ever goes back and edits, and it cannot be translated.

## Decision

- **Every user-facing string is a Catalog entry**, one directory of TOML per
  module and language (`xy/i18nstrings/ru/board.toml`), one file per Surface,
  a `common.toml` for shared words. Keys name the string's role, not its text.
  Out of scope: developer-facing English (protocol errors, logs) and the
  chgksuite parity labels in `xy/internal/chgk/i18n`, which mirror upstream.
- **TOML, not YAML**, so no module gains a dependency: the hand-rolled parser
  from `chgk/i18n` moves to `dopecore/i18nstrings` and serves both.
- **Generated, not interpreted.** `scripts/i18nstringsgen` reads the TOML and
  emits one typed function per string in Go (`ru_gen.go`) and TS
  (`i18nstrings_ru_gen.ts`), one file per language, gated by `generate-check`
  like the other `*_gen` targets. Nothing parses a template at runtime, and a
  wrong id or missing argument fails to compile in both languages.
- **Templates use `text/template` syntax, restricted.** Field access
  (`{{.n}}`) and one `plural` function are allowed; the generator parses with
  `text/template/parse` and rejects any other node. Russian one/few/many lives
  once in `dopecore` and once in `dopeuikit`, replacing xy's three copies.
- **One `Strings` struct per module, one value per language.** Callers write
  `s.Board.Delete.Confirm(n)` off a `Strings` value. Today every one of them
  reads the module's default — `i18nstrings.Default` in Go, `i18nstrings.ts`
  in the browser — so adding a language is an edit to that one file, and the
  type already allows a value to be threaded the day a reader gets to choose.
- **Errors a person may read are `UserError`s** built from the catalog where
  they arise; the HTTP edge shows those verbatim and maps everything else to
  one generic line plus a log entry.
- **`.dopeui` references strings as `@surface.group.key`**, resolved by the
  expander against the page's `Strings`; the validator rejects unknown ids.
- **A Cyrillic lint** fails on any Cyrillic outside the catalogs, the generated
  files, the tests and an explicit allowlist. The allowlist is how we track the
  migration: strings are moved Surface by Surface, word for word, and any
  rewording happens afterwards in the TOML. The lint works line by line, so it
  cannot tell a Russian comment from a Russian string. We translated the
  comments rather than adding exemptions for them.

## Considered

- YAML: needs a dependency in every Go module.
- Runtime lookup by id (`t("board.delete.confirm")`, i18next, go-i18n): one
  interpreter per language, ids checked only when the string is reached, and
  a rebuild is needed to ship a change anyway since the catalog is embedded.
- Using the source string as the key, as gettext does: that keeps the Russian
  in the code, which is exactly what this change exists to remove.
- Keying by domain concept instead of by Surface: that would scatter one screen
  across several files, and «Сохранить» does not belong to any concept.
