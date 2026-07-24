package main

import "strings"

// A TypeScript lexer, enough to find class names by token sequence rather than
// by text. typescript-go's own scanner would be the right tool, but every
// package of it lives under internal/ and Go refuses the import; its RPC server
// speaks an unversioned wire format. Tokens are what regexes actually lacked
// here — `activeClass = "results"` matched for want of a boundary, and
// toggle()'s force argument read as a class for want of argument splitting.
// A full AST would add little beyond this: the remaining hard cases (aliasing a
// classList into a local, computed names) need types, not a parse tree.

type tokKind int

const (
	tkPunct tokKind = iota
	tkIdent
	tkString // quoted or template; val is the static text only
	tkNumber
)

type tsToken struct {
	kind tokKind
	val  string
}

func (t tsToken) isPunct(v string) bool { return t.kind == tokKind(tkPunct) && t.val == v }

// Operators are matched greedily so that `===` never reads as `=`.
var operators = []string{
	">>>=", "...", "===", "!==", "**=", "<<=", ">>=", ">>>", "&&=", "||=", "??=",
	"==", "!=", "<=", ">=", "&&", "||", "??", "?.", "=>", "+=", "-=", "*=", "/=",
	"%=", "&=", "|=", "^=", "++", "--", "**", "<<", ">>",
}

// After these, a `/` opens a regex literal rather than dividing.
var regexKeyword = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true, "of": true,
	"new": true, "delete": true, "void": true, "case": true, "do": true,
	"else": true, "yield": true, "await": true, "throw": true,
}

func lexTS(src string) []tsToken {
	var toks []tsToken
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			if j := strings.Index(src[i+2:], "*/"); j < 0 {
				i = len(src)
			} else {
				i += j + 4
			}
		case c == '/' && regexCanStart(toks):
			i = skipRegex(src, i)
		case c == '"' || c == '\'':
			val, next := lexQuoted(src, i)
			toks, i = append(toks, tsToken{tkString, val}), next
		case c == '`':
			val, inner, next := lexTemplate(src, i)
			toks = append(append(toks, tsToken{tkString, val}), inner...)
			i = next
		case isIdentStart(c):
			j := i + 1
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			toks, i = append(toks, tsToken{tkIdent, src[i:j]}), j
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (isIdentPart(src[j]) || src[j] == '.') {
				j++
			}
			toks, i = append(toks, tsToken{tkNumber, src[i:j]}), j
		default:
			op := operatorAt(src, i)
			toks, i = append(toks, tsToken{tkPunct, op}), i+len(op)
		}
	}
	return toks
}

func operatorAt(src string, i int) string {
	for _, op := range operators {
		if strings.HasPrefix(src[i:], op) {
			return op
		}
	}
	return src[i : i+1]
}

func regexCanStart(toks []tsToken) bool {
	if len(toks) == 0 {
		return true
	}
	switch t := toks[len(toks)-1]; t.kind {
	case tkString, tkNumber:
		return false
	case tkIdent:
		return regexKeyword[t.val]
	default:
		return t.val != ")" && t.val != "]" && t.val != "}"
	}
}

func skipRegex(src string, i int) int {
	i++ // opening slash
	inClass := false
	for i < len(src) {
		switch src[i] {
		case '\\':
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				i++
				for i < len(src) && isIdentPart(src[i]) { // flags
					i++
				}
				return i
			}
		case '\n':
			return i
		}
		i++
	}
	return i
}

func lexQuoted(src string, i int) (string, int) {
	quote := src[i]
	start := i + 1
	for j := start; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case '\n':
			return src[start:j], j
		case quote:
			return src[start:j], j + 1
		}
	}
	return src[start:], len(src)
}

// lexTemplate returns the template's static text with interpolations replaced
// by a space — `grid-match ${status}` yields "grid-match ", never "grid-match"
// glued to whatever follows — plus the tokens of the interpolated code.
func lexTemplate(src string, i int) (string, []tsToken, int) {
	var static strings.Builder
	var inner []tsToken
	i++ // opening backtick
	for i < len(src) {
		switch c := src[i]; {
		case c == '\\':
			i += 2
		case c == '`':
			return static.String(), inner, i + 1
		case c == '$' && i+1 < len(src) && src[i+1] == '{':
			expr, next := matchBrace(src, i+1)
			inner = append(inner, lexTS(expr)...)
			static.WriteByte(' ')
			i = next
		default:
			static.WriteByte(c)
			i++
		}
	}
	return static.String(), inner, i
}

// matchBrace returns the text between the brace at open and its partner, and
// the index just past it, skipping over strings and nested templates.
func matchBrace(src string, open int) (string, int) {
	depth := 0
	for i := open; i < len(src); {
		switch src[i] {
		case '{':
			depth++
			i++
		case '}':
			depth--
			i++
			if depth == 0 {
				return src[open+1 : i-1], i
			}
		case '"', '\'':
			_, i = lexQuoted(src, i)
		case '`':
			_, _, i = lexTemplate(src, i)
		case '\\':
			i += 2
		default:
			i++
		}
	}
	return src[open+1:], len(src)
}

// splitArgs splits the argument list whose "(" sits at open into top-level
// arguments, so a nested call or object cannot be mistaken for a separator.
func splitArgs(toks []tsToken, open int) [][]tsToken {
	var args [][]tsToken
	var cur []tsToken
	depth := 0
	for i := open; i < len(toks); i++ {
		t := toks[i]
		if t.kind == tkPunct {
			switch t.val {
			case "(", "[", "{":
				depth++
				if depth == 1 {
					continue
				}
			case ")", "]", "}":
				depth--
				if depth == 0 {
					return append(args, cur)
				}
			case ",":
				if depth == 1 {
					args = append(args, cur)
					cur = nil
					continue
				}
			}
		}
		cur = append(cur, t)
	}
	return append(args, cur)
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }
