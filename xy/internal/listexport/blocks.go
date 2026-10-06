package listexport

import (
	"regexp"
	"sort"
	"strings"

	"xy/internal/chgk/fsource"
)

// The 4s line layer the assembly reads a description through (chgk.ts
// parseBlocks and its helpers).

// markers, longest first, so "!=" is not read as "!". The table is fsource's,
// the same one markers_gen.ts is generated from.
var markers = func() []struct{ marker, kind string } {
	types := fsource.MarkerTypes()
	out := make([]struct{ marker, kind string }, 0, len(types))
	for m, k := range types {
		out = append(out, struct{ marker, kind string }{m, k})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].marker) != len(out[j].marker) {
			return len(out[i].marker) > len(out[j].marker)
		}
		return out[i].marker < out[j].marker
	})
	return out
}()

// typeMarker is the reverse table: the marker a block of each kind is written
// with, first spelling wins (chgk.ts TYPE_MARKER).
var typeMarker = func() map[string]string {
	out := map[string]string{}
	for _, m := range markers {
		if _, seen := out[m.kind]; !seen {
			out[m.kind] = m.marker
		}
	}
	return out
}()

// preTypes are the blocks that, standing before the question, are pre-markup.
var preTypes = map[string]bool{
	"setcounter": true, "number": true, "meta": true, "section": true,
	"heading": true, "ljheading": true, "editor": true, "date": true,
}

var versionLine = regexp.MustCompile(`^\(hidden-comment\s+xy-version:([^()]*)\)$`)

// VersionLineName reads a Version separator line's name: "" for the unnamed
// form, and ok=false when the line is no separator at all.
func VersionLineName(line string) (name string, ok bool) {
	m := versionLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// MatchMarker splits a line that opens a 4s element into the element's kind
// and the text after the marker.
func MatchMarker(line string) (kind, rest string, ok bool) {
	for _, m := range markers {
		if line == m.marker {
			return m.kind, "", true
		}
		if strings.HasPrefix(line, m.marker+" ") {
			return m.kind, line[len(m.marker)+1:], true
		}
	}
	return "", "", false
}

// opensQuestion reports whether a line opens a rung of a ladder: a `№` or a
// `№№`. A blank line before one is the only thing that ends a question there.
func opensQuestion(line string) bool {
	kind, _, ok := MatchMarker(line)
	return ok && (kind == "number" || kind == "setcounter")
}

type block struct{ kind, text string }

// parseBlocks splits a description into its 4s elements: a marker opens one,
// every other line continues it, and text before any marker is a "pre" block.
// Version separators are xy's own metadata and drop out here, which is what
// makes every reader but the card editor see a versioned Card as its Version 1.
func parseBlocks(desc string) []block {
	var blocks []block
	cur := -1
	for _, line := range strings.Split(desc, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if _, isVersion := VersionLineName(line); isVersion {
			continue
		}
		if kind, rest, ok := MatchMarker(line); ok {
			blocks = append(blocks, block{kind, rest})
			cur = len(blocks) - 1
			continue
		}
		if cur >= 0 {
			blocks[cur].text += "\n" + line
			continue
		}
		blocks = append(blocks, block{"pre", line})
		cur = len(blocks) - 1
	}
	out := blocks[:0]
	for _, b := range blocks {
		b.text = strings.TrimSpace(b.text)
		if b.text != "" || b.kind != "pre" {
			out = append(out, b)
		}
	}
	return out
}

func rawLine(b block) string {
	marker, ok := typeMarker[b.kind]
	if !ok {
		return b.text
	}
	if b.text == "" {
		return marker
	}
	return marker + " " + b.text
}

// questionText is a question's text without its marker: the "?" block, else the
// text before any marker, else the whole description.
func questionText(desc string) string {
	blocks := parseBlocks(desc)
	for _, b := range blocks {
		if b.kind == "question" {
			return b.text
		}
	}
	for _, b := range blocks {
		if b.kind == "pre" {
			return b.text
		}
	}
	return strings.TrimSpace(desc)
}

// rawQuestion is the question block as written, handout bracket included.
func rawQuestion(desc string) string {
	for _, b := range parseBlocks(desc) {
		if b.kind == "question" || b.kind == "pre" {
			return b.text
		}
	}
	return ""
}
