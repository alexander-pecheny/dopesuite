package typo

import "golang.org/x/text/unicode/norm"

// nfd is Python's unicodedata.normalize("NFD", …) for a single rune: it splits a
// precomposed letter (e.g. "á") into base + combining mark so cyrLatCheckChar can
// spot a Latin vowel carrying a stress accent.
func nfd(r rune) string { return norm.NFD.String(string(r)) }
