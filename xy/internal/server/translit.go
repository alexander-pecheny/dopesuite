package server

import (
	"strings"
	"unicode"
)

// translitTable spells Cyrillic in Latin letters, the way a Russian name is
// usually written in a passport. It is what a download is called when the client
// only reads the ASCII filename= (export.ts does), so a Cyrillic list name
// arrives as a readable Latin one rather than as a row of underscores.
var translitTable = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "yo",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "",
	'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
	'і': "i", 'ї': "yi", 'є': "ye", 'ґ': "g", 'ў': "u",
	'«': "", '»': "", '„': "", '“': "", '”': "", '–': "-", '—': "-", '№': "N",
}

// translit folds name to printable ASCII: Cyrillic goes through translitTable,
// stress marks are dropped, and anything else that is not ASCII becomes "_".
// A capital becomes a capital: all capitals when the word around it is
// capitalised (SHCHI), otherwise only the first letter (Shchi).
func translit(name string) string {
	rs := []rune(name)
	var b strings.Builder
	for i, r := range rs {
		switch {
		case r < 0x80:
			b.WriteRune(r)
			continue
		case unicode.Is(unicode.Mn, r):
			continue
		}
		lat, ok := translitTable[unicode.ToLower(r)]
		if !ok {
			b.WriteByte('_')
			continue
		}
		if !unicode.IsUpper(r) || lat == "" {
			b.WriteString(lat)
			continue
		}
		if shouting(rs, i) {
			b.WriteString(strings.ToUpper(lat))
		} else {
			b.WriteString(strings.ToUpper(lat[:1]) + lat[1:])
		}
	}
	return b.String()
}

// shouting reports whether the capital at i stands in an all-caps word: the
// next letter is a capital too, or there is none and the one before is.
func shouting(rs []rune, i int) bool {
	next := func(j int) (rune, bool) {
		for ; j < len(rs); j++ {
			if !unicode.Is(unicode.Mn, rs[j]) {
				return rs[j], true
			}
		}
		return 0, false
	}
	if n, ok := next(i + 1); ok && unicode.IsLetter(n) {
		return unicode.IsUpper(n)
	}
	return i > 0 && unicode.IsUpper(rs[i-1])
}
