package inline

import (
	"regexp"
	"strings"
)

// The "screen mode" transforms (composer_common.py): the host's copy of a
// question carries stress accents and bracketed reading instructions; the
// screen's copy carries neither. A bracket whose body opens with the handout
// marker is a handout — the players see it, so it and everything inside it
// survive both passes. The same port lives in web/ts/chgk.ts for the card editor.

// reHandoutShort is regexes_ru.json's handout_short.
var reHandoutShort = regexp.MustCompile(`^Р[Аа][Зз][Дд][Аа][Тт]`)

// IsHandoutBody reports whether a square-bracket body is a handout marker.
func IsHandoutBody(body string) bool { return reHandoutShort.MatchString(body) }

// ReplaceEscaped unescapes \[ and \] (common.py replace_escaped).
func ReplaceEscaped(s string) string {
	return strings.NewReplacer(`\[`, "[", `\]`, "]").Replace(s)
}

func isEscapedBracket(r []rune, i int) bool {
	return r[i] == '\\' && i+1 < len(r) && (r[i+1] == '[' || r[i+1] == ']')
}

// matchingSquareBracket returns the index of the "]" closing the "[" at i,
// counting nesting and skipping escaped brackets, or -1.
func matchingSquareBracket(r []rune, i int) int {
	if i >= len(r) || r[i] != '[' {
		return -1
	}
	depth := 0
	for i < len(r) {
		if isEscapedBracket(r, i) {
			i += 2
			continue
		}
		switch r[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// RemoveAccents drops combining stress marks everywhere but inside handout
// brackets.
func RemoveAccents(s string) string {
	r := []rune(s)
	var b strings.Builder
	prev := 0
	for i := 0; i < len(r); {
		if isEscapedBracket(r, i) {
			i += 2
			continue
		}
		if r[i] != '[' {
			i++
			continue
		}
		end := matchingSquareBracket(r, i)
		if end < 0 {
			i++
			continue
		}
		if IsHandoutBody(string(r[i+1 : end])) {
			b.WriteString(dropAccents(string(r[prev:i])))
			b.WriteString(string(r[i : end+1]))
			prev = end + 1
		}
		i = end + 1
	}
	b.WriteString(dropAccents(string(r[prev:])))
	return b.String()
}

func dropAccents(s string) string { return strings.ReplaceAll(s, "́", "") }

// RemoveSquareBrackets drops the host-only bracketed notes, keeping handout
// brackets, then unescapes the literal ones.
func RemoveSquareBrackets(s string) string {
	r := []rune(s)
	var out []rune
	removed := false
	for i := 0; i < len(r); {
		if isEscapedBracket(r, i) {
			out = append(out, r[i], r[i+1])
			i += 2
			continue
		}
		if r[i] != '[' {
			out = append(out, r[i])
			i++
			continue
		}
		end := matchingSquareBracket(r, i)
		if end < 0 {
			out = append(out, r[i])
			i++
			continue
		}
		if IsHandoutBody(string(r[i+1 : end])) {
			out = append(out, r[i:end+1]...)
		} else {
			for len(out) > 0 && out[len(out)-1] == ' ' {
				out = out[:len(out)-1]
			}
			removed = true
		}
		i = end + 1
	}
	res := string(out)
	if removed {
		res = strings.TrimSpace(res)
	}
	return ReplaceEscaped(res)
}
