package typstdoc

import (
	"fmt"
	"strconv"
	"strings"

	"xy/internal/chgk/i18n"
	"xy/internal/chgk/inline"
)

// Config is pdf_config.toml: the typography and page setup a project can
// override with --pdf_config. The defaults are template.docx's, transcribed
// (twips → mm/pt), so the PDF and the .docx lay out the same.
//
// TopEdge and BottomEdge are load-bearing, not taste. By default typst measures
// a line box from cap-height to baseline, so a block's height leaves out its
// descenders — fine when blocks are separated by par.spacing, ruinous here,
// because Word's paragraphs are flush and consecutive blocks would then overlap
// by a descender. Fixed em edges keep the line box the same whatever the font;
// the defaults are Noto Sans's own metrics, so its output is unchanged.
type Config struct {
	TopEdge, BottomEdge, LeadingPt           float64
	MarginVMM, MarginHMM                     float64
	BodyPt, Heading1Pt, Heading2Pt, SourcePt float64
	HeadingAbove, HeadingBelow               float64
	QuestionAbove, AnswerAbove, SourceGap    float64
}

// DefaultConfig is the pdf_config.toml chgksuite ships.
func DefaultConfig() Config {
	return Config{
		TopEdge: 1.07, BottomEdge: -0.29, LeadingPt: 0,
		MarginVMM: 25.4, MarginHMM: 19.05,
		BodyPt: 12, Heading1Pt: 16, Heading2Pt: 14, SourcePt: 10,
		HeadingAbove: 12, HeadingBelow: 3,
		QuestionAbove: 18, AnswerAbove: 6,
		// The shrunk source block starts one body line below: 2pt × Noto Sans's
		// 1.362em line box (ascender 1.069 + descender 0.293).
		SourceGap: 2.72,
	}
}

// Options are the switches `compose pdf` takes.
type Options struct {
	// Device is --device: Desktop is the A4 layout that mirrors the .docx,
	// Mobile a page sized for a phone screen. Empty means Desktop.
	Device Device
	// Config is --pdf_config. The zero value is DefaultConfig.
	Config Config
	// Font is --font. Empty is the bundled Noto Sans, the only face the wasm
	// typst has loaded unless the caller loaded more.
	Font string
	// Language is --language, which typst hyphenates and quotes by. Empty is ru.
	Language string
	// NoBreak is --replace_no_break_spaces / --replace_no_break_hyphens.
	NoBreak inline.NoBreak
}

func (o Options) resolve() Options {
	if o.Config == (Config{}) {
		o.Config = DefaultConfig()
	}
	if o.Device == "" {
		o.Device = Desktop
	}
	if o.Font == "" {
		o.Font = fontFamily
	}
	if o.Language == "" {
		o.Language = i18n.DefaultLanguage
	}
	return o
}

// lang is typst's two-letter language tag, which is what chgksuite passes it.
func lang(language string) string {
	if len(language) > 2 {
		return language[:2]
	}
	return language
}

// ParseConfig reads the subset of TOML pdf_config.toml is: tables of numbers.
// A key the file leaves out keeps its default.
func ParseConfig(text string) (Config, error) {
	c := DefaultConfig()
	fields := map[string]*float64{
		"line_box.top_edge": &c.TopEdge, "line_box.bottom_edge": &c.BottomEdge,
		"line_box.leading_pt": &c.LeadingPt,
		"page.margin_v_mm":    &c.MarginVMM, "page.margin_h_mm": &c.MarginHMM,
		"font.body_pt": &c.BodyPt, "font.heading1_pt": &c.Heading1Pt,
		"font.heading2_pt": &c.Heading2Pt, "font.source_pt": &c.SourcePt,
		"spacing.heading_above": &c.HeadingAbove, "spacing.heading_below": &c.HeadingBelow,
		"spacing.question_above": &c.QuestionAbove, "spacing.answer_above": &c.AnswerAbove,
		"spacing.source_gap": &c.SourceGap,
	}
	table := ""
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			table = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("строка %d: не пара ключ = значение", n+1)
		}
		field, known := fields[table+"."+strings.TrimSpace(key)]
		if !known {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return c, fmt.Errorf("строка %d: %w", n+1, err)
		}
		*field = v
	}
	return c, nil
}
