// Package inline is the 4s inline layer shared by every exporter: it tokenizes a
// 4s text element into runs (bold/italic/img/screen/hyperlink/…), applies the
// typographic passes (backtick stress accents, non-breaking-space gluing,
// non-breaking hyphens), and parses the (img …) sizing options.
//
// It was lifted verbatim out of internal/chgk/docx when the typst PDF exporter
// (internal/chgk/typstdoc) needed the same text pipeline: the two exports must
// agree character-for-character on where the nbsp/nbhyphen land, so there can only
// be one copy of this.
package inline

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// run is one inline element of 4s markup (port of chgksuite _parse_4s_elem /
// xy chgk.js parse4sElem). Kind is "" for plain text, or one of italic/bold/
// underline/italicbold/boldunderline/italicboldunderline/strike/sc/img/screen/
// linebreak/pagebreak/hyperlink.
type Run struct {
	Kind      string
	Text      string
	ForPrint  string // screen runs only
	ForScreen string // screen runs only
}

const (
	underscorePlaceholder = "\x00UNDERSCORE\x00"
	tildePlaceholder      = "\x00TILDE\x00"
)

var rePercent = regexp.MustCompile(`(%[0-9a-fA-F]{2})+`)

// backtickReplace: a backtick before a Cyrillic letter adds a combining stress
// accent to it (chgksuite backtick_replace); otherwise the backtick is dropped.
func BacktickReplace(s string) string {
	r := []rune(s)
	var out []rune
	for i := 0; i < len(r); i++ {
		if r[i] != '`' {
			out = append(out, r[i])
			continue
		}
		if i+1 >= len(r) {
			continue
		}
		next := r[i+1]
		if isCyrillic(next) {
			out = append(out, next, '́')
			i++
		}
		// non-cyrillic after backtick: drop the backtick, keep scanning
	}
	return string(out)
}

func isCyrillic(r rune) bool {
	return (r >= 'а' && r <= 'я') || r == 'ё' || (r >= 'А' && r <= 'Я') || r == 'Ё'
}

func HTTPURLSpans(s string) [][2]int {
	var spans [][2]int
	r := []rune(s)
	i := 0
	for i < len(r) {
		if hasPrefixAt(r, i, "http://") || hasPrefixAt(r, i, "https://") {
			j := i + 1
			bracket := 0
			for j < len(r) && !(isSpace(r[j]) || (r[j] == ')' && bracket == 0)) {
				if r[j] == '(' {
					bracket++
				} else if r[j] == ')' && bracket > 0 {
					bracket--
				}
				j++
			}
			end := j
			if j-1 >= 0 && (r[j-1] == ',' || r[j-1] == '.' || r[j-1] == ';') {
				end = j - 1
			}
			spans = append(spans, [2]int{i, end})
			i = j
		} else {
			i++
		}
	}
	return spans
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v'
}

func hasPrefixAt(r []rune, i int, p string) bool {
	pr := []rune(p)
	if i+len(pr) > len(r) {
		return false
	}
	for k, c := range pr {
		if r[i+k] != c {
			return false
		}
	}
	return true
}

func findMatchingBracket(r []rune, index int) int {
	open := r[index]
	var clo rune
	switch open {
	case '(':
		clo = ')'
	case '[':
		clo = ']'
	case '{':
		clo = '}'
	default:
		return -1
	}
	counter := 0
	for i := index; i < len(r); i++ {
		if r[i] == open {
			counter++
		}
		if r[i] == clo {
			counter--
			if counter == 0 {
				return i
			}
		}
	}
	return -1
}

func findNextUnescaped(r []rune, index, length int) int {
	j := index + length
	for j < len(r) {
		if r[j] == '\\' && j+2 < len(r) {
			j += 2
		}
		if j+length <= len(r) && string(r[j:j+length]) == string(r[index:index+length]) {
			return j
		}
		j++
	}
	return -1
}

func ProcessEsc(s string) string {
	s = strings.ReplaceAll(s, "\\_", "_")
	s = strings.ReplaceAll(s, "\\.", ".")
	s = strings.ReplaceAll(s, underscorePlaceholder, "_")
	s = strings.ReplaceAll(s, tildePlaceholder, "~")
	return s
}

// parse4sElem tokenizes a 4s inline string into runs.
func Parse4sElem(s string) []Run {
	s = strings.ReplaceAll(s, "\\_", underscorePlaceholder)
	s = strings.ReplaceAll(s, "\\~", tildePlaceholder)

	// protect underscores/tildes inside URLs
	{
		r := []rune(s)
		var b strings.Builder
		last := 0
		for _, sp := range HTTPURLSpans(s) {
			b.WriteString(string(r[last:sp[0]]))
			seg := string(r[sp[0]:sp[1]])
			seg = strings.ReplaceAll(seg, "_", underscorePlaceholder)
			seg = strings.ReplaceAll(seg, "~", tildePlaceholder)
			b.WriteString(seg)
			last = sp[1]
		}
		b.WriteString(string(r[last:]))
		s = b.String()
	}

	// percent-decode (longest matches first)
	grs := dedup(rePercent.FindAllString(s, -1))
	sort.SliceStable(grs, func(i, j int) bool { return len(grs[i]) > len(grs[j]) })
	for _, gr := range grs {
		if dec, err := url.QueryUnescape(gr); err == nil {
			s = strings.ReplaceAll(s, gr, dec)
		}
	}

	r := []rune(s)
	var topart []int
	i := 0
	for i < len(r) {
		if r[i] == '_' || r[i] == '~' {
			j := i + 1
			for j < len(r) && r[j] == r[i] {
				j++
			}
			length := j - i
			topart = append(topart, i)
			nxt := findNextUnescaped(r, i, length)
			if nxt != -1 {
				topart = append(topart, nxt+length)
				i = nxt + length + 1
				continue
			}
		}
		if r[i] == '(' && hasPrefixAt(r, i, "(img") {
			topart = append(topart, i)
			if close := findMatchingBracket(r, i); close != -1 {
				topart = append(topart, close+1)
				i = close
			}
		}
		if r[i] == '(' && hasPrefixAt(r, i, "(screen") {
			topart = append(topart, i)
			if close := findMatchingBracket(r, i); close != -1 {
				topart = append(topart, close+1)
				i = close
			}
		}
		if hasPrefixAt(r, i, "(PAGEBREAK)") {
			topart = append(topart, i, i+len("(PAGEBREAK)"))
		}
		if hasPrefixAt(r, i, "(LINEBREAK)") {
			topart = append(topart, i, i+len("(LINEBREAK)"))
		}
		if hasPrefixAt(r, i, "http://") || hasPrefixAt(r, i, "https://") {
			topart = append(topart, i)
			j := i + 1
			bracket := 0
			for j < len(r) && !(isSpace(r[j]) || (r[j] == ')' && bracket == 0)) {
				if r[j] == '(' {
					bracket++
				} else if r[j] == ')' && bracket > 0 {
					bracket--
				}
				j++
			}
			if j-1 >= 0 && (r[j-1] == ',' || r[j-1] == '.' || r[j-1] == ';') {
				topart = append(topart, j-1)
			} else {
				topart = append(topart, j)
			}
			i = j
		}
		i++
	}

	sort.Ints(topart)
	segs := partition(r, topart)
	var parts []Run
	for _, seg := range segs {
		text := strings.ReplaceAll(seg, "敥", "")
		parts = append(parts, Run{Kind: "", Text: text})
	}

	for idx := range parts {
		p := &parts[idx]
		if p.Text == "" {
			continue
		}
		if strings.HasPrefix(p.Text, "_") && strings.HasSuffix(p.Text, "_") {
			pr := []rune(p.Text)
			j := 1
			for j < len(pr) && pr[j] == '_' && pr[len(pr)-j-1] == '_' {
				j++
			}
			p.Text = string(pr[j : len(pr)-j])
			switch {
			case j == 1:
				p.Kind = "italic"
			case j == 2:
				p.Kind = "bold"
			case j == 3:
				p.Kind = "underline"
			case j == 4:
				p.Kind = "italicbold"
			case j == 5:
				p.Kind = "boldunderline"
			default:
				p.Kind = "italicboldunderline"
			}
		}
		if strings.HasPrefix(p.Text, "~") && strings.HasSuffix(p.Text, "~") {
			p.Kind = "strike"
			p.Text = strings.TrimPrefix(strings.TrimSuffix(p.Text, "~"), "~")
		}
		if p.Text == "(PAGEBREAK)" {
			p.Kind = "pagebreak"
			p.Text = ""
		}
		if p.Text == "(LINEBREAK)" {
			p.Kind = "linebreak"
			p.Text = ""
		}
		if len([]rune(p.Text)) > 4 && strings.HasPrefix(p.Text, "(img") {
			if !strings.HasSuffix(p.Text, ")") {
				p.Text += ")"
			}
			pr := []rune(p.Text)
			p.Text = string(pr[4 : len(pr)-1])
			p.Kind = "img"
		}
		if len([]rune(p.Text)) > 7 && strings.HasPrefix(p.Text, "(screen") {
			if !strings.HasSuffix(p.Text, ")") {
				p.Text += ")"
			}
			pr := []rune(p.Text)
			inner := string(pr[8 : len(pr)-1])
			fp, fs, _ := strings.Cut(inner, "|")
			p.ForPrint = ProcessEsc(fp)
			p.ForScreen = ProcessEsc(fs)
			p.Kind = "screen"
			p.Text = ""
			continue
		}
		if strings.HasPrefix(p.Text, "http://") || strings.HasPrefix(p.Text, "https://") {
			p.Kind = "hyperlink"
		}
		if len([]rune(p.Text)) > 3 && strings.HasPrefix(p.Text, "(sc") {
			if !strings.HasSuffix(p.Text, ")") {
				p.Text += ")"
			}
			pr := []rune(p.Text)
			p.Text = string(pr[3 : len(pr)-1])
			p.Kind = "sc"
		}
		p.Text = ProcessEsc(p.Text)
	}
	return parts
}

func partition(r []rune, indices []int) []string {
	bounds := append([]int{0}, indices...)
	bounds = append(bounds, len(r))
	var out []string
	for k := 0; k+1 < len(bounds); k++ {
		out = append(out, string(r[bounds[k]:bounds[k+1]]))
	}
	return out
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
