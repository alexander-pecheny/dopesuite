// Package roman writes a list number in Roman numerals, as Word numbers a
// list and as chgksuite numbers a slide.
package roman

import "strings"

var numerals = []struct {
	value int
	text  string
}{
	{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"},
	{50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
}

// Upper is n in upper-case Roman numerals, "" for a number below 1.
func Upper(n int) string {
	var b strings.Builder
	for _, nm := range numerals {
		for n >= nm.value {
			b.WriteString(nm.text)
			n -= nm.value
		}
	}
	return b.String()
}
