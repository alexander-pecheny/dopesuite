package docx

import "strings"

// A plain styled document, for the callers that are not the question export:
// `board download --si` and `--qb` build a .docx out of headings and paragraphs
// the way python-docx's add_paragraph(style=…) does, over the same template.

// Block is one paragraph: its style ("" is the body style, "Heading1",
// "Heading2", …) and its text, which may be empty for a blank line.
type Block struct {
	Style string
	Text  string
	Bold  bool
}

// Heading returns a heading block at the given level.
func Heading(level int, text string) Block {
	return Block{Style: "Heading" + string(rune('0'+level)), Text: text}
}

// Paragraph returns a body-style block.
func Paragraph(text string) Block { return Block{Text: text} }

// Simple renders the blocks into the template, as Export renders a package.
// opts is read for --font and --docx_template only.
func Simple(blocks []Block, opts Options) ([]byte, error) {
	e := &exporter{nextRel: 7, nextDoc: 1000, opts: opts}
	var body strings.Builder
	for _, b := range blocks {
		p := &para{style: b.Style}
		if b.Text != "" {
			kind := ""
			if b.Bold {
				kind = "bold"
			}
			// The text may carry its own newlines; python-docx keeps them in one
			// run and Word shows them as line breaks.
			for i, line := range strings.Split(b.Text, "\n") {
				if i > 0 {
					p.runs = append(p.runs, brk())
				}
				p.addRaw(line, kind)
			}
		}
		body.WriteString(p.xml())
	}
	return e.repackage(body.String())
}
