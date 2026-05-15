package main

import (
	"bytes"
	"html/template"

	"github.com/yuin/goldmark"
)

var markdownEngine = goldmark.New()

func renderMarkdown(source string) template.HTML {
	if source == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := markdownEngine.Convert([]byte(source), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(source))
	}
	return template.HTML(buf.String())
}
