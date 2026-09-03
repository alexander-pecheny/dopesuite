// Package i18nstrings is the kit's Catalog (root docs/adr/0006): every
// user-facing string it shows, one TOML file per Surface under ru/, generated
// into the typed Strings below. Edit the TOML, then `just generate-strings`.
//
//go:generate go -C ../../scripts/i18nstringsgen run . -dir dopeuikit/i18nstrings -ts dopeuikit/assets/ts
package i18nstrings

// Default is what a caller renders in when nothing chose a language.
var Default = RU
