// Package i18nstrings is chgksuite's Catalog (root docs/adr/0006): every
// user-facing string the chgk packages, the CLI and the browser's question
// renderer (web/ts) show, one TOML file per Surface under ru/, generated into
// the typed Strings below and its TypeScript twin in web/ts. Edit the TOML,
// then `just generate-strings`.
//
//go:generate go -C ../../scripts/i18nstringsgen run . -dir chgksuite/i18nstrings -ts chgksuite/web/ts
package i18nstrings

// Default is what a caller renders in when nothing chose a language.
var Default = RU
