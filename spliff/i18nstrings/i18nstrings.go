// Package i18nstrings is Spliff's Catalog (root docs/adr/0006): every
// user-facing string it shows, one TOML file per Surface under en/, generated
// into the typed Strings below. Edit the TOML, then `just generate-strings`.
//
// Spliff is English-only (spliff/CONTEXT.md), so there is no ru/ at all and the
// generator is told so with -default-lang.
//
//go:generate go -C ../scripts/i18nstringsgen run . -dir spliff/i18nstrings -ts spliff/spliff/web/ts -default-lang en
package i18nstrings

// Default is what a caller renders in when nothing chose a language. For Spliff
// that is always English.
var Default = EN
