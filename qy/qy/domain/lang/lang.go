// Package lang names the languages a nominated question may be written in:
// Russian, Ukrainian or Belarusian, the ones the contest has received. A
// question in another language than Russian is translated for the jury
// (domain/translate). Telling which one a question is in is the separate
// qy-langdetect binary's job (platform/langdetect), so the server does not
// carry its language models.
package lang

// The languages, as stored.
const (
	Russian    = "ru"
	Ukrainian  = "uk"
	Belarusian = "be"
)

// All is every language a question may be in, Russian first.
var All = []string{Russian, Ukrainian, Belarusian}

// Valid says whether l is one of All.
func Valid(l string) bool { return l == Russian || l == Ukrainian || l == Belarusian }
