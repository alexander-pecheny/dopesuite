package tgbot

import (
	"encoding/json"
	"strings"
)

// RichMessage is a message in Telegram's rich formatting: a tree of blocks
// (paragraphs, lists, quotations, collapsible details, tables, media with
// captions) whose leaves are rich text. A post written that way arrives with
// no Text at all.
type RichMessage struct {
	Blocks []json.RawMessage `json:"blocks"`
}

// PlainText flattens the message into plain text, one line per paragraph:
// what a reader sees with every collapsed block opened, without the
// formatting. A details block's summary is left out, since it is the
// label of the toggle ("Ответ") and not part of what it hides.
func (r *RichMessage) PlainText() string {
	if r == nil {
		return ""
	}
	var lines []string
	for _, b := range r.Blocks {
		lines = appendBlock(lines, b)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// richNode holds every field a block or a list item can carry that has text
// in it; which are set depends on the type.
type richNode struct {
	Type   string            `json:"type"`
	Text   json.RawMessage   `json:"text"`
	Blocks []json.RawMessage `json:"blocks"`
	Items  []richNode        `json:"items"`
	Label  string            `json:"label"`
	Cells  [][]struct {
		Text json.RawMessage `json:"text"`
	} `json:"cells"`
	Caption json.RawMessage `json:"caption"`
	Credit  json.RawMessage `json:"credit"`
}

func appendBlock(lines []string, raw json.RawMessage) []string {
	var b richNode
	if json.Unmarshal(raw, &b) != nil {
		return lines
	}
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			lines = append(lines, s)
		}
	}
	add(richText(b.Text))
	for _, sub := range b.Blocks {
		lines = appendBlock(lines, sub)
	}
	for _, item := range b.Items {
		var inner []string
		for _, sub := range item.Blocks {
			inner = appendBlock(inner, sub)
		}
		add(strings.TrimSpace(item.Label + " " + strings.Join(inner, "\n")))
	}
	for _, row := range b.Cells {
		cells := make([]string, 0, len(row))
		for _, c := range row {
			cells = append(cells, strings.TrimSpace(richText(c.Text)))
		}
		add(strings.Join(cells, "\t"))
	}
	add(captionText(b.Caption))
	add(richText(b.Credit))
	return lines
}

// captionText reads a caption, which is a RichBlockCaption on media and plain
// rich text on a table.
func captionText(raw json.RawMessage) string {
	var c struct {
		Text   json.RawMessage `json:"text"`
		Credit json.RawMessage `json:"credit"`
	}
	if json.Unmarshal(raw, &c) == nil && c.Text != nil {
		return strings.TrimSpace(richText(c.Text) + " " + richText(c.Credit))
	}
	return richText(raw)
}

// richText is a RichText's characters: a string, an array of rich texts, or
// a styled span whose own text holds them. A span with nothing but an
// expression (a formula) gives its LaTeX source.
func richText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) == nil {
		var sb strings.Builder
		for _, p := range parts {
			sb.WriteString(richText(p))
		}
		return sb.String()
	}
	var span struct {
		Text       json.RawMessage `json:"text"`
		Expression string          `json:"expression"`
	}
	if json.Unmarshal(raw, &span) != nil {
		return ""
	}
	if span.Text == nil {
		return span.Expression
	}
	return richText(span.Text)
}
