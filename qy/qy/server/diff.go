package qyserver

import "strings"

// diffContext is how many unchanged lines show around a change.
const diffContext = 3

type diffLine struct {
	Op   string // "+", "-" or " "
	Text string
}

// lineDiff is a longest-common-subsequence diff, from studchr's site_v2. Pages
// run to a few thousand lines, so the quadratic table is affordable.
func lineDiff(before, after string) []diffLine {
	a, b := splitLines(before), splitLines(after)
	lcs := make([][]int32, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []diffLine
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, diffLine{" ", a[i]})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, diffLine{"-", a[i]})
			i++
		default:
			out = append(out, diffLine{"+", b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, diffLine{"-", a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, diffLine{"+", b[j]})
	}
	return trimContext(out, diffContext)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// trimContext keeps n unchanged lines around each change and folds the rest
// into a "…" marker.
func trimContext(lines []diffLine, n int) []diffLine {
	keep := make([]bool, len(lines))
	for i, l := range lines {
		if l.Op == " " {
			continue
		}
		for k := max(0, i-n); k <= min(len(lines)-1, i+n); k++ {
			keep[k] = true
		}
	}
	var out []diffLine
	for i, l := range lines {
		if keep[i] {
			out = append(out, l)
		} else if len(out) == 0 || out[len(out)-1].Op != "…" {
			out = append(out, diffLine{"…", ""})
		}
	}
	return out
}
