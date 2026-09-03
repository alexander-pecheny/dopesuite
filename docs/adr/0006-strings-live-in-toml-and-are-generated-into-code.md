---
status: accepted
date: 2026-09-03
---

# User-facing strings live in TOML catalogs and are generated into typed code

Both apps carried their copy inline: about 4,000 lines of Russian across Go
handlers, dope's Go-built pages, TS modules, `.dopeui` attributes and export
headers, with `http.Error(w, err.Error())` in dope showing internal errors to
the browser. Prose written at the call site is prose nobody edits, and it
cannot be translated.

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
- **One `Strings` struct per module, one value per language.** Callers hold a
  `Strings` value (`i18nstrings.Default` where no reader chose a language) and
  write `s.Board.Delete.Confirm(n)`. Adding a language touches the place that
  picks it, not the call sites.
- **Errors a person may read are `UserError`s** built from the catalog where
  they arise; the HTTP edge shows those verbatim and maps everything else to
  one generic line plus a log entry.
- **`.dopeui` references strings as `@surface.group.key`**, resolved by the
  expander against the page's `Strings`; the validator rejects unknown ids.
- **A Cyrillic lint** fails on any Cyrillic outside the catalogs, generated
  files, tests and an explicit allowlist. The allowlist is the migration's
  burn-down: strings move Surface by Surface, verbatim, and rewording happens
  afterwards in TOML.

## Considered

- YAML: needs a dependency in every Go module.
- Runtime lookup by id (`t("board.delete.confirm")`, i18next, go-i18n): one
  interpreter per language, ids checked only when the string is reached, and
  a rebuild is needed to ship a change anyway since the catalog is embedded.
- Source string as key (gettext): keeps the Russian in the code, which is what
  the change exists to remove.
- Keying by domain concept rather than Surface: scatters one screen across
  files, and «Сохранить» belongs to no concept.
