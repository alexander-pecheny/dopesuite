package pptx

import (
	"sort"
	"strconv"
	"strings"
)

// The getters below are chgksuite's, one for one: each falls back the way the
// Python does, which is why they read the config rather than a struct of
// defaults — an absent key and a zero are different things here.

func (c *Config) fontTable() map[string]any { return c.table("font") }

// fontSize is _get_font_size: a named size, or the chain of fallbacks that ends
// at the caller's.
func (c *Config) fontSize(key string, fallback float64) float64 {
	if f, ok := tableNum(c.fontTable(), key); ok {
		return f
	}
	switch key {
	case "question_size":
		if f, ok := c.num("force_text_size_question"); ok {
			return f
		}
		return c.fontSize("default_size", fallback)
	case "answer_size":
		if f, ok := c.num("force_text_size_answer"); ok {
			return f
		}
		return c.fontSize("default_size", fallback)
	case "tour_size":
		return c.fontSize("default_size", fallback)
	case "default_size":
		if f, ok := c.num("force_text_size_question"); ok {
			return f
		}
		if f, ok := tableNum(c.table("text_size_grid"), "default"); ok && f != 0 {
			return f
		}
	}
	return fallback
}

func (c *Config) fontName() string { return tableStr(c.fontTable(), "name", "") }

// headingFontName is _get_heading_font_name.
func (c *Config) headingFontName() string {
	if s := tableStr(c.fontTable(), "heading_name", ""); s != "" {
		return s
	}
	return c.fontName()
}

type gridElement struct{ length, size float64 }

// gridElements is _get_grid_elements: the per-role table of "text this long gets
// a font this big", or the shared one.
func (c *Config) gridElements(role string) []gridElement {
	grid := c.table("text_size_grid")
	raw, ok := grid[role+"_elements"].([]any)
	if !ok {
		raw, ok = grid["elements"].([]any)
		if !ok {
			return nil
		}
	}
	var out []gridElement
	for _, item := range raw {
		t, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, gridElement{tableNumOr(t, "length", 0), tableNumOr(t, "size", 0)})
	}
	return out
}

// gridFontSize is _get_grid_font_size: the first band the text is short enough
// for, or the role's smallest.
func (c *Config) gridFontSize(role, text string, fallback float64) float64 {
	elements := c.gridElements(role)
	if len(elements) == 0 {
		return fallback
	}
	sort.SliceStable(elements, func(i, j int) bool { return elements[i].length < elements[j].length })
	length := float64(len([]rune(text)))
	for _, e := range elements {
		if length <= e.length {
			return e.size
		}
	}
	grid := c.table("text_size_grid")
	if f, ok := tableNum(grid, role+"_smallest"); ok {
		return f
	}
	if f, ok := tableNum(grid, "smallest"); ok {
		return f
	}
	return fallback
}

// fontSizeForText is _get_font_size_for_text.
func (c *Config) fontSizeForText(role, text, key string, fallback float64) float64 {
	return c.gridFontSize(role, text, c.fontSize(key, fallback))
}

func (c *Config) smallestSize() float64 {
	return tableNumOr(c.table("text_size_grid"), "smallest", 14)
}

func (c *Config) textbox() map[string]any       { return c.table("textbox") }
func (c *Config) numberTextbox() map[string]any { return c.table("number_textbox") }
func (c *Config) handout() map[string]any       { return c.table("handout") }
func (c *Config) list() map[string]any          { return c.table("list") }

func (c *Config) includeHandoutLabel() bool  { return tableBool(c.handout(), "include_label", false) }
func (c *Config) handoutImageScale() float64 { return tableNumOr(c.handout(), "image_scale", 1) }
func (c *Config) handoutSpaceAfter() float64 { return tableNumOr(c.handout(), "space_after", 18) }

// handoutTextSpaceAfter is _get_pptx_handout_text_space_after: the newer key
// wins, the older stands in for it.
func (c *Config) handoutTextSpaceAfter() float64 {
	if f, ok := tableNum(c.handout(), "text_space_after"); ok {
		return f
	}
	return tableNumOr(c.handout(), "space_after", 18)
}

func (c *Config) handoutFontSize() float64 {
	if f, ok := tableNum(c.handout(), "font_size"); ok {
		return f
	}
	return c.fontSize("tour_size", 42)
}

func (c *Config) disableShrinkFit() bool    { return c.boolean("disable_shrink_fit", false) }
func (c *Config) overlayImageAndText() bool { return c.boolean("overlay_image_and_text", false) }
func (c *Config) formatLinks() bool         { return c.boolean("format_links", true) }
func (c *Config) addPlug() bool             { return c.boolean("add_plug", false) }
func (c *Config) addComment() bool          { return c.boolean("add_comment", false) }
func (c *Config) addZachet() bool           { return c.boolean("add_zachet", false) }
func (c *Config) addSource() bool           { return c.boolean("add_source", false) }
func (c *Config) addAuthor() bool           { return c.boolean("add_author", false) }
func (c *Config) textIsDuplicated() bool    { return c.boolean("text_is_duplicated", false) }
func (c *Config) disableAutolayout() bool   { return c.boolean("disable_autolayout", false) }
func (c *Config) blankLineBeforeItems() bool {
	return tableBool(c.list(), "blank_line_before_items", true)
}

// addHandoutOnSeparateSlide is _add_handout_on_separate_slide: absent means yes.
func (c *Config) addHandoutOnSeparateSlide() bool {
	return c.boolean("add_handout_on_separate_slide", true)
}

func (c *Config) templateVersion() int {
	if f, ok := c.num("template_version"); ok {
		return int(f)
	}
	return 1
}

func (c *Config) layoutIndex(key string, fallback int) int {
	if f, ok := c.num(key); ok {
		return int(f)
	}
	return fallback
}

// lineSpacingConfigured is _line_spacing_configured.
func (c *Config) lineSpacingConfigured() bool {
	font := c.fontTable()
	if tableBool(font, "fixed_line_spacing", false) {
		return true
	}
	if _, ok := tableNum(font, "line_spacing_multiplier"); ok {
		return true
	}
	for k, v := range font {
		if strings.HasPrefix(k, "fixed_line_spacing_") && v != nil {
			return true
		}
	}
	return false
}

// listMarker is _format_list_marker: the numbering style's Nth marker.
func (c *Config) listMarker(n int) string {
	style := tableStr(c.list(), "numbering_style", "1.")
	if strings.Contains(style, "{n}") {
		return strings.ReplaceAll(style, "{n}", strconv.Itoa(n))
	}
	if style == "" {
		style = "1."
	}
	kind, suffix := style[:1], style[1:]
	switch kind {
	case "1":
		return strconv.Itoa(n) + suffix
	case "a":
		return alphaMarker(n, false) + suffix
	case "A":
		return alphaMarker(n, true) + suffix
	case "i":
		return romanMarker(n, false) + suffix
	case "I":
		return romanMarker(n, true) + suffix
	}
	return strconv.Itoa(n) + style
}

func alphaMarker(n int, upper bool) string {
	out := ""
	for n > 0 {
		n--
		out = string(rune('a'+n%26)) + out
		n /= 26
	}
	if upper {
		return strings.ToUpper(out)
	}
	return out
}

func romanMarker(n int, upper bool) string {
	numerals := []struct {
		value int
		text  string
	}{
		{1000, "m"}, {900, "cm"}, {500, "d"}, {400, "cd"}, {100, "c"}, {90, "xc"},
		{50, "l"}, {40, "xl"}, {10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"},
	}
	out := ""
	for _, nm := range numerals {
		for n >= nm.value {
			out += nm.text
			n -= nm.value
		}
	}
	if upper {
		return strings.ToUpper(out)
	}
	return out
}

// serviceSlideIndices is _slide_indices_from_config: one index or a list of them.
func (c *Config) serviceSlideIndices(key string) []int {
	svc := c.table("service_slides")
	if svc == nil {
		return nil
	}
	switch v := svc[key].(type) {
	case float64:
		return []int{int(v)}
	case []any:
		var out []int
		for _, x := range v {
			if f, ok := x.(float64); ok {
				out = append(out, int(f))
			}
		}
		return out
	}
	return nil
}

func (c *Config) skipGeneratedTitle() bool {
	return tableBool(c.table("service_slides"), "skip_generated_title", false)
}

// numberedTourStubIndices is _numbered_tour_stub_indices, which reads either
// spelling of the key.
func (c *Config) numberedTourStubIndices() []int {
	if idx := c.serviceSlideIndices("numbered_tours_stubs"); len(idx) > 0 {
		return idx
	}
	return c.serviceSlideIndices("numbered_tour_stubs")
}

func (c *Config) configuredServiceSlideIndices() []int {
	var out []int
	for _, key := range []string{
		"intro", "between_tours", "final",
		"numbered_tours_stubs", "numbered_tour_stubs", "remove",
	} {
		out = append(out, c.serviceSlideIndices(key)...)
	}
	return out
}
