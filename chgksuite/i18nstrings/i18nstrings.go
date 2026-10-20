// Package i18nstrings is chgksuite's Catalog (root docs/adr/0006): every
// user-facing string the chgk packages and the CLI show, one TOML file per
// Surface under ru/, generated into the typed Strings below. Edit the TOML,
// then `just generate-strings`.
//
//go:generate go -C ../../scripts/i18nstringsgen run . -dir chgksuite/i18nstrings
package i18nstrings

// Default is what a caller renders in when nothing chose a language.
var Default = RU
