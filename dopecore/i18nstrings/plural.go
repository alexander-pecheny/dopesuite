package i18nstrings

// Lang is a catalog language tag, the name of a directory under i18nstrings/.
type Lang string

// Plural picks the form of a counted noun — the Go half of the generator's
// emitTSPlural, which has to be a switch because it ships to the browser, so
// this one is too. A language with no rule of its own gets the English
// one/other shape out of one and many.
func Plural(lang Lang, n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	if lang == "ru" {
		if rest := n % 100; rest >= 11 && rest <= 14 {
			return many
		}
		switch n % 10 {
		case 1:
			return one
		case 2, 3, 4:
			return few
		}
		return many
	}
	if n == 1 {
		return one
	}
	return many
}
