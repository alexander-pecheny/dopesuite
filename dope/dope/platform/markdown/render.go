// Package markdown renders host-authored markdown (fest descriptions, etc.) to
// safe HTML. It wraps goldmark with the custom :::details disclosure block and
// deliberately leaves raw-HTML passthrough disabled, so untrusted input cannot
// inject markup.
package markdown

import (
	"bytes"
	"html/template"

	"github.com/yuin/goldmark"
)

var engine = goldmark.New(
	goldmark.WithExtensions(detailsExtension{}),
)

// Render converts markdown source to HTML. On any conversion error it falls back
// to the HTML-escaped source so output is always safe to embed.
func Render(source string) template.HTML {
	if source == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := engine.Convert([]byte(source), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(source))
	}
	return template.HTML(buf.String())
}
