package main

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// detailsExtension adds a fenced block that compiles to a native HTML5
// <details>/<summary> disclosure, WITHOUT enabling goldmark's unsafe raw-HTML
// passthrough. Hosts write:
//
//	:::details Состав судей
//	Иван **Петров**, Мария Сидорова
//	:::
//
// The opening fence is three-or-more colons immediately followed by the
// keyword `details`; the rest of that line becomes the summary label. The body
// (until a closing line of only colons) is parsed as ordinary markdown. Because
// the <details>/<summary> tags are emitted by this trusted renderer — not lifted
// verbatim from the source — the summary is HTML-escaped and any raw HTML inside
// the body is still dropped, so this is safe for the public, host-authored fest
// descriptions.
type detailsExtension struct{}

const detailsKeyword = "details"

// defaultDetailsSummary labels a disclosure whose opening fence gave no summary
// text.
const defaultDetailsSummary = "Подробнее"

func (detailsExtension) Extend(md goldmark.Markdown) {
	md.Parser().AddOptions(parser.WithBlockParsers(
		util.Prioritized(&detailsParser{}, 100),
	))
	md.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(&detailsRenderer{}, 100),
	))
}

// detailsNode is the AST node for a :::details block. Summary holds the raw
// (un-escaped) label text; escaping happens at render time.
type detailsNode struct {
	ast.BaseBlock
	Summary string
}

var kindDetails = ast.NewNodeKind("Details")

func (n *detailsNode) Kind() ast.NodeKind { return kindDetails }

func (n *detailsNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Summary": n.Summary}, nil)
}

// fenceColonRun returns the length of the leading run of ':' starting at the
// line's first non-space offset, plus the trimmed remainder after it. off is the
// block offset (first non-space column); a negative off (blank line) yields 0.
func fenceColonRun(line []byte, off int) (colons int, rest string) {
	if off < 0 || off >= len(line) {
		return 0, ""
	}
	i := off
	for i < len(line) && line[i] == ':' {
		i++
	}
	return i - off, strings.TrimSpace(string(line[i:]))
}

type detailsParser struct{}

func (p *detailsParser) Trigger() []byte { return []byte{':'} }

func (p *detailsParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()
	colons, rest := fenceColonRun(line, pc.BlockOffset())
	if colons < 3 || !strings.HasPrefix(rest, detailsKeyword) {
		return nil, parser.NoChildren
	}
	// Require the keyword to stand alone or be followed by space — so a future
	// `:::detailsomething` doesn't accidentally open a disclosure.
	after := rest[len(detailsKeyword):]
	if after != "" && !strings.HasPrefix(after, " ") {
		return nil, parser.NoChildren
	}
	node := &detailsNode{Summary: strings.TrimSpace(after)}
	reader.Advance(segment.Len())
	return node, parser.HasChildren
}

func (p *detailsParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, segment := reader.PeekLine()
	if colons, rest := fenceColonRun(line, pc.BlockOffset()); colons >= 3 && rest == "" {
		reader.Advance(segment.Len())
		return parser.Close
	}
	return parser.Continue | parser.HasChildren
}

func (p *detailsParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}

func (p *detailsParser) CanInterruptParagraph() bool { return true }

func (p *detailsParser) CanAcceptIndentedLine() bool { return false }

type detailsRenderer struct{}

func (r *detailsRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindDetails, r.render)
}

func (r *detailsRenderer) render(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	node := n.(*detailsNode)
	if entering {
		summary := node.Summary
		if summary == "" {
			summary = defaultDetailsSummary
		}
		_, _ = w.WriteString("<details>\n<summary>")
		_, _ = w.Write(util.EscapeHTML([]byte(summary)))
		_, _ = w.WriteString("</summary>\n")
	} else {
		_, _ = w.WriteString("</details>\n")
	}
	return ast.WalkContinue, nil
}
