package i18nstrings

// Lang is a catalog language tag, the name of a directory under i18nstrings/.
type Lang string

// The Russian plural rule reads the last one and two digits of n.
const (
	lastDigitMod     = 10
	lastTwoDigitsMod = 100
	ruTeenFirst      = 11 // 11..14 always take "many"
	ruTeenLast       = 14
	ruFewLast        = 4 // a last digit of 2..4 takes "few"
)

// Plural picks the form of a counted noun — the Go half of the generator's
// emitTSPlural, which has to be a switch because it ships to the browser, so
// this one is too. A language with no rule of its own gets the English
// one/other shape out of one and many.
func Plural(lang Lang, n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	if lang == "ru" {
		if rest := n % lastTwoDigitsMod; rest >= ruTeenFirst && rest <= ruTeenLast {
			return many
		}
		switch last := n % lastDigitMod; {
		case last == 1:
			return one
		case last >= 2 && last <= ruFewLast:
			return few
		}
		return many
	}
	if n == 1 {
		return one
	}
	return many
}
