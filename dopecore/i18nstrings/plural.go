package i18nstrings

// Lang is a catalog language tag, the name of a directory under i18nstrings/.
type Lang string

// Rule picks a form for n: 0 = one, 1 = few, 2 = many.
type Rule func(n int) int

var rules = map[Lang]Rule{"ru": russian}

// Register teaches Plural a language's rule. Nothing registers at init but
// Russian; a second language adds itself here or from its own package.
func Register(lang Lang, r Rule) { rules[lang] = r }

// Plural picks the form of a counted noun. A language with no rule gets the
// English one/other shape out of one and many.
func Plural(lang Lang, n int, one, few, many string) string {
	forms := [3]string{one, few, many}
	if r, ok := rules[lang]; ok {
		return forms[r(n)]
	}
	if n == 1 || n == -1 {
		return one
	}
	return many
}

func russian(n int) int {
	if n < 0 {
		n = -n
	}
	if m := n % 100; m >= 11 && m <= 14 {
		return 2
	}
	switch n % 10 {
	case 1:
		return 0
	case 2, 3, 4:
		return 1
	}
	return 2
}
