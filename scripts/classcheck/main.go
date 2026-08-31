// The class-inventory check. The design system's Vocabulary is closed on the
// Go side — an unknown primitive or prop is a compile error — but the
// stylesheets and the TypeScript that emits class names are checked against
// nothing, and that is where ~72% of the class names live. This walks both
// halves and fails when they drift apart:
//
//	orphan rule   a class is styled but nothing emits it
//	dead name     a class is emitted but nothing styles it
//
// Run from the repo root: go -C scripts/classcheck run .
//
// The two directions deliberately use different precision. An orphan-rule
// false positive tells you to delete live styling, so "emitted" is read
// generously — any string literal anywhere in the repo counts. A dead-name
// false positive only costs a line in allow.txt, so "emitted" there is read
// strictly, from unambiguous class positions only — token sequences in
// TypeScript, AST nodes in Go.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var stylesheets = []string{
	"dopeuikit/assets/core.css",
	"dope/dope/web/assets/static/styles.css",
	"xy/web/assets/static/styles.css",
}

var sourceRoots = []string{"dopeuikit", "dopecore", "dope", "xy"}

var skipDirs = map[string]bool{
	"node_modules": true,
	"dist":         true,
	".git":         true,
	"fonts":        true,
}

// A class name in this codebase is lowercase kebab. Holding the check to that
// shape keeps the strict direction from flagging arbitrary string tokens.
var classShape = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

var (
	reSelectorClass = regexp.MustCompile(`\.(-?[A-Za-z_][\w-]*)`)
	// Markup a module builds as a string is HTML, not TypeScript; a pattern is
	// the right tool once the lexer has handed over the string's contents.
	reClassAttr = regexp.MustCompile(`\bclass="([^"]*)"`)
	reToken     = regexp.MustCompile(`[A-Za-z_][\w-]*`)
)

type sites map[string]map[string]bool // class name -> set of files

func (s sites) add(name, file string) {
	if s[name] == nil {
		s[name] = map[string]bool{}
	}
	s[name][file] = true
}

func (s sites) files(name string) []string {
	out := make([]string, 0, len(s[name]))
	for f := range s[name] {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func main() {
	// Runs via `go -C scripts/classcheck run .`, whose cwd is the module dir;
	// every path below is repo-root-relative (same idiom as scripts/webbuild).
	if _, err := os.Stat(stylesheets[0]); err != nil {
		if err := os.Chdir("../.."); err != nil {
			fatal("%v", err)
		}
		if _, err := os.Stat(stylesheets[0]); err != nil {
			fatal("run from the repo root or via `go -C scripts/classcheck run .`")
		}
	}

	styled := sites{}
	geometry, layout := 0, 0
	layoutBaseline, err := readAllow("scripts/classcheck/layout-baseline.txt")
	if err != nil {
		fatal("%v", err)
	}
	for _, sheet := range stylesheets {
		src, err := os.ReadFile(sheet)
		if err != nil {
			fatal("%v", err)
		}
		for _, name := range styledClasses(string(src)) {
			styled.add(name, sheet)
		}
		geometry += reportGridLiterals(sheet, string(src))
		layout += reportLayoutClasses(sheet, string(src), layoutBaseline)
	}

	literals := sites{} // generous: any class-shaped token inside a string literal
	emitted := sites{}  // strict: unambiguous class positions in TypeScript and Go

	for _, root := range sourceRoots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(path)
			if ext != ".ts" && ext != ".go" && ext != ".dopeui" && ext != ".html" {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scan(string(src), path, ext, literals, emitted)
			return nil
		})
		if err != nil {
			fatal("%v", err)
		}
	}

	allowed, err := readAllow("scripts/classcheck/allow.txt")
	if err != nil {
		fatal("%v", err)
	}

	var orphans, dead []string
	var composed int
	for name := range styled {
		if len(literals[name]) > 0 || allowed[name] {
			continue
		}
		if composedFromPrefix(name, literals) {
			composed++
			continue
		}
		orphans = append(orphans, name)
	}
	for name := range emitted {
		if len(styled[name]) > 0 || allowed[name] {
			continue
		}
		// No exemption for names used as JS selectors: a class is for styling,
		// a data-* attribute is for behaviour. Mixing them is what made the dead
		// ones unfindable, so an emitted class must have a rule.
		dead = append(dead, name)
	}
	sort.Strings(orphans)
	sort.Strings(dead)

	for _, name := range orphans {
		fmt.Printf("%s: orphan rule — .%s is styled but nothing emits it\n",
			strings.Join(styled.files(name), ", "), name)
	}
	for _, name := range dead {
		fmt.Printf("%s: dead name — %q is emitted but nothing styles it\n",
			strings.Join(emitted.files(name), ", "), name)
	}

	if n := len(orphans) + len(dead) + geometry + layout; n > 0 {
		fmt.Fprintf(os.Stderr, "\nclasscheck: %d orphan rule(s), %d dead name(s), %d Сетка geometry literal(s), "+
			"%d re-invented layout class(es).\n"+
			"Delete them, or record the exception in scripts/classcheck/allow.txt "+
			"(layout-baseline.txt for the last kind) with a reason.\n",
			len(orphans), len(dead), geometry, layout)
		os.Exit(1)
	}
	// The exemption is the check's one blind spot, so it is reported every run
	// rather than hiding a shrinking guarantee behind a green line.
	fmt.Printf("classcheck: %d classes, both halves agree "+
		"(%d unprovable — built from a composed prefix)\n", len(styled), composed)
}

// styledClasses returns every class named in a selector. It walks the sheet
// rather than regexing it whole so that declaration values — url(logo.png),
// font stacks — can never be mistaken for selectors.
func styledClasses(src string) []string {
	src = stripComments(src)
	var out []string
	var prelude strings.Builder
	var quote byte
	for i := 0; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '{':
			for _, m := range reSelectorClass.FindAllStringSubmatch(prelude.String(), -1) {
				out = append(out, m[1])
			}
			prelude.Reset()
		case '}', ';':
			prelude.Reset()
		default:
			prelude.WriteByte(c)
		}
	}
	return out
}

func stripComments(src string) string {
	var b strings.Builder
	for {
		i := strings.Index(src, "/*")
		if i < 0 {
			b.WriteString(src)
			return b.String()
		}
		b.WriteString(src[:i])
		j := strings.Index(src[i+2:], "*/")
		if j < 0 {
			return b.String()
		}
		src = src[i+2+j+2:]
	}
}

func scan(src, path, ext string, literals, emitted sites) {
	if ext != ".ts" {
		for _, lit := range goStringLiterals(src) {
			record(lit, path, literals)
		}
		if ext == ".go" && !strings.HasSuffix(path, "_test.go") {
			scanGo(src, path, emitted)
		}
		return
	}

	toks := lexTS(src)
	for _, t := range toks {
		if t.kind == tkString {
			record(t.val, path, literals)
		}
	}
	emitSites(toks, path, emitted)
}

func record(lit, path string, literals sites) {
	for _, tok := range reToken.FindAllString(lit, -1) {
		literals.add(tok, path)
	}
}

// emitSites finds the token sequences that unambiguously put a class name on an
// element: `.className =`, `.classList.add(…)`, `setAttribute("class", …)`, and
// class="…" inside markup a module builds as a string.
func emitSites(toks []tsToken, path string, emitted sites) {
	for i, t := range toks {
		if t.kind == tkString {
			for _, m := range reClassAttr.FindAllStringSubmatch(t.val, -1) {
				addStatic(emitted, m[1], path)
			}
			continue
		}
		if t.kind != tkIdent {
			continue
		}
		switch t.val {
		case "className":
			// The leading dot is what keeps this off `activeClass = "results"`.
			if i == 0 || !toks[i-1].isPunct(".") || i+2 >= len(toks) {
				continue
			}
			if !toks[i+1].isPunct("=") && !toks[i+1].isPunct("+=") {
				continue
			}
			if toks[i+2].kind == tkString {
				addStatic(emitted, toks[i+2].val, path)
			}
		case "add", "remove", "toggle":
			if i < 2 || !toks[i-1].isPunct(".") || toks[i-2].val != "classList" {
				continue
			}
			if i+1 >= len(toks) || !toks[i+1].isPunct("(") {
				continue
			}
			args := splitArgs(toks, i+1)
			// add/remove are variadic over class names; toggle's second
			// argument is a force flag, whose strings are conditions.
			if t.val == "toggle" && len(args) > 1 {
				args = args[:1]
			}
			addStringArgs(emitted, args, path)
		case "setAttribute":
			if i+1 >= len(toks) || !toks[i+1].isPunct("(") {
				continue
			}
			args := splitArgs(toks, i+1)
			if len(args) == 2 && len(args[0]) == 1 && args[0][0].kind == tkString && args[0][0].val == "class" {
				addStringArgs(emitted, args[1:], path)
			}
		}
	}
}

func addStringArgs(emitted sites, args [][]tsToken, path string) {
	for _, arg := range args {
		for _, t := range arg {
			if t.kind == tkString {
				addStatic(emitted, t.val, path)
			}
		}
	}
}

// addStatic records the class names a literal definitely carries. The lexer has
// already blanked interpolations, so `grid-match ${status}` contributes
// grid-match and one name this check cannot know.
func addStatic(emitted sites, lit, path string) {
	for _, tok := range strings.Fields(lit) {
		if classShape.MatchString(tok) {
			emitted.add(tok, path)
		}
	}
}

// composedFromPrefix reports whether some literal is a prefix of name ending at
// a "-" boundary: helpers.go builds "u-align-" + value from a closed enum, so
// u-align-center appears nowhere in full. Borrowed from xy's own dead-CSS test
// (xy/internal/ui/unusedcss_test.go), which checks xy's layer the same way.
// It can hide a genuinely dead name sharing a composed prefix — the trade this
// direction is meant to make, since the alternative is deleting live styling.
func composedFromPrefix(name string, literals sites) bool {
	for i := 1; i < len(name); i++ {
		if name[i] == '-' && len(literals[name[:i+1]]) > 0 {
			return true
		}
	}
	return false
}

func goStringLiterals(src string) []string {
	var out []string
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '"':
			lit, next := lexQuoted(src, i)
			out, i = append(out, lit), next-1
		case '`':
			if j := strings.IndexByte(src[i+1:], '`'); j >= 0 {
				out, i = append(out, src[i+1:i+1+j]), i+1+j
			}
		}
	}
	return out
}

func readAllow(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]bool{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("%s: %q needs a reason after the name", path, line)
		}
		out[name] = true
	}
	return out, s.Err()
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "classcheck: "+format+"\n", args...)
	os.Exit(2)
}
