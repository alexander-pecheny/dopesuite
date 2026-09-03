// Package i18nstrings is xy's Catalog (root docs/adr/0006): every user-facing
// string xy shows, one TOML file per Surface under ru/, generated into the
// typed Strings below. Edit the TOML, then `just generate-strings`.
//
//go:generate go -C ../../scripts/i18nstringsgen run . -dir xy/i18nstrings -ts xy/web/ts
package i18nstrings

// Default is what a caller renders in when nothing chose a language.
var Default = RU
