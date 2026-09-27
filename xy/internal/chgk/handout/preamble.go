package handout

import (
	"strings"
	"unicode"

	corei18n "pecheny.me/dopecore/i18nstrings"

	xystrings "xy/i18nstrings"
)

// preambleMarker is the first line of the block that sets defaults for every
// handout in the file.
const preambleMarker = "///preamble"

// How many columns and rows a grid has depends on the handout in it.
var preambleExcluded = map[string]bool{"for_question": true, "image": true, "columns": true, "rows": true}

// ApplyPreamble is utils.apply_preamble: it drops the /// comment lines, and
// writes the settings of a leading ///preamble block into every handout that
// does not set them itself. An explicit font or size in a overrides the
// preamble's.
func ApplyPreamble(hndt string, a Args) (string, error) {
	if !strings.Contains(hndt, "///") {
		return hndt, nil
	}
	blocks := splitBlocks(hndt)
	var keys []string
	vals := map[string]string{}
	if len(blocks) > 0 && firstLine(blocks[0]) == preambleMarker {
		for _, line := range strings.Split(blocks[0], "\n") {
			if strings.TrimSpace(line) == "" || isComment(line) {
				continue
			}
			key, val, ok := strings.Cut(line, ":")
			if !ok || !reservedWords[key] || preambleExcluded[key] {
				return "", corei18n.User(xystrings.Default.Docs.Handout.PreambleSetting(strings.TrimSpace(line)))
			}
			if (key == "font_family" && a.Font != "") || (key == "font_size" && a.FontSize != 0) {
				continue
			}
			if _, seen := vals[key]; !seen {
				keys = append(keys, key)
			}
			vals[key] = strings.TrimSpace(val)
		}
		blocks = blocks[1:]
	}
	out := make([]string, len(blocks))
	for i, raw := range blocks {
		var lines []string
		own := map[string]bool{}
		content := false
		for _, line := range strings.Split(raw, "\n") {
			if isComment(line) {
				continue
			}
			lines = append(lines, line)
			own[strings.SplitN(line, ":", 2)[0]] = true
			content = content || strings.TrimSpace(line) != ""
		}
		if content {
			var head []string
			for _, k := range keys {
				if !own[k] {
					head = append(head, k+": "+vals[k])
				}
			}
			lines = append(head, lines...)
		}
		out[i] = strings.Join(lines, "\n")
	}
	return strings.Join(out, "\n---\n"), nil
}

func isComment(line string) bool {
	return strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "///")
}

func firstLine(block string) string {
	for _, line := range strings.Split(block, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
