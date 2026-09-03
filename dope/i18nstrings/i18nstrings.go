// Package i18nstrings is dope's Catalog (root docs/adr/0006): every user-facing
// string dope shows, one TOML file per Surface under ru/, generated into the
// typed Strings below. Edit the TOML, then `just generate-strings`.
//
//go:generate go -C ../../scripts/i18nstringsgen run . -dir dope/i18nstrings -ts dope/dope/web/ts
package i18nstrings

// Default is what a caller renders in when nothing chose a language.
var Default = RU
