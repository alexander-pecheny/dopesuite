// Package markdown renders the CMS pages. Raw HTML passes through: only admins
// write pages, and the archived years are HTML to begin with.
package markdown

import (
	"bytes"
	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.Linkify, extension.Footnote),
	goldmark.WithParserOptions(parser.WithAutoHeadingID(), parser.WithAttribute()),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

func Render(src string) (string, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var varRe = regexp.MustCompile(`\{\{\s*([a-z_]+)\s*\}\}`)

// Expand fills in {{name}} for every name vars knows and leaves the rest as
// written, so a page can mention braces it does not mean as a variable.
func Expand(src string, vars map[string]string) string {
	if len(vars) == 0 {
		return src
	}
	return varRe.ReplaceAllStringFunc(src, func(m string) string {
		if v, ok := vars[varRe.FindStringSubmatch(m)[1]]; ok {
			return v
		}
		return m
	})
}
