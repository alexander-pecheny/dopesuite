package server

import (
	"bytes"
	"encoding/json"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/handout"
	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"

	xystrings "xy/i18nstrings"
)

// handoutPage is a tour's page setup for its handouts: chgksuite's handout
// flags, kept as JSON on the list, or on its group, which a grouped list
// follows. A field left out is chgksuite's default. The page sends it along
// with every render, so the handout endpoints never read the list.
type handoutPage struct {
	PaperWidth          *int     `json:"paperwidth,omitempty"`
	PaperHeight         *int     `json:"paperheight,omitempty"`
	MarginTop           *int     `json:"margin_top,omitempty"`
	MarginBottom        *int     `json:"margin_bottom,omitempty"`
	MarginLeft          *int     `json:"margin_left,omitempty"`
	MarginRight         *int     `json:"margin_right,omitempty"`
	Font                string   `json:"font,omitempty"`
	FontSize            *int     `json:"font_size,omitempty"`
	BoxWidth            *float64 `json:"boxwidth,omitempty"`
	TikzMM              *float64 `json:"tikz_mm,omitempty"`
	NoAutoResize        bool     `json:"no_auto_resize_images,omitempty"`
	ShrinkPercent       *float64 `json:"image_shrink_percent,omitempty"`
	MinResizeImage      *float64 `json:"min_resize_image,omitempty"`
	BottomSpaceRowRatio *float64 `json:"image_bottom_space_row_ratio,omitempty"`
}

// The bounds a page setup is held to, in mm, pt and shares.
const (
	minPaperMM    = 50
	maxPaperMM    = 1000
	maxMarginMM   = 200
	minFontSize   = 4
	maxFontSize   = 96
	maxGapMM      = 50
	maxShrinkPct  = 90
	maxSpaceRatio = 10
)

// parseHandoutPage reads a page setup, refusing a field it does not know and a
// value out of bounds. "" is the defaults.
func parseHandoutPage(s string) (handoutPage, error) {
	var p handoutPage
	if s == "" {
		return p, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil || !p.valid() {
		return p, corei18n.User(xystrings.Default.Server.Handouts.BadPage())
	}
	return p, nil
}

func (p handoutPage) valid() bool {
	within := func(v *int, lo, hi int) bool { return v == nil || (*v >= lo && *v <= hi) }
	withinF := func(v *float64, lo, hi float64) bool { return v == nil || (*v > lo && *v <= hi) }
	return within(p.PaperWidth, minPaperMM, maxPaperMM) && within(p.PaperHeight, minPaperMM, maxPaperMM) &&
		within(p.MarginTop, 0, maxMarginMM) && within(p.MarginBottom, 0, maxMarginMM) &&
		within(p.MarginLeft, 0, maxMarginMM) && within(p.MarginRight, 0, maxMarginMM) &&
		within(p.FontSize, minFontSize, maxFontSize) && fontFamily.MatchString(p.Font) &&
		withinF(p.BoxWidth, 0, maxPaperMM) && (p.TikzMM == nil || (*p.TikzMM >= 0 && *p.TikzMM <= maxGapMM)) &&
		withinF(p.ShrinkPercent, 0, maxShrinkPct) && withinF(p.MinResizeImage, 0, 1) &&
		withinF(p.BottomSpaceRowRatio, 0, maxSpaceRatio)
}

// args is the page setup laid over chgksuite's defaults.
func (p handoutPage) args() handout.Args {
	a := handout.DefaultArgs()
	set := func(dst *int, v *int) {
		if v != nil {
			*dst = *v
		}
	}
	setF := func(dst *float64, v *float64) {
		if v != nil {
			*dst = *v
		}
	}
	set(&a.PaperWidth, p.PaperWidth)
	set(&a.PaperHeight, p.PaperHeight)
	set(&a.MarginTop, p.MarginTop)
	set(&a.MarginBottom, p.MarginBottom)
	set(&a.MarginLeft, p.MarginLeft)
	set(&a.MarginRight, p.MarginRight)
	set(&a.FontSize, p.FontSize)
	a.Font = p.Font
	a.BoxWidth, a.TikzMM = p.BoxWidth, p.TikzMM
	a.Resize.Off = p.NoAutoResize
	setF(&a.Resize.ShrinkPercent, p.ShrinkPercent)
	setF(&a.Resize.MinResizeImage, p.MinResizeImage)
	setF(&a.Resize.BottomSpaceRowRatio, p.BottomSpaceRowRatio)
	return a
}

// normalized is the setup as it is stored: compact, and NULL when it holds
// nothing but defaults.
func (p handoutPage) normalized() (any, error) {
	b, err := json.Marshal(p)
	if err != nil || string(b) == "{}" {
		return nil, err
	}
	return string(b), nil
}

// pageArgs reads the page field the handout renders send.
func pageArgs(form *memForm) (handout.Args, error) {
	p, err := parseHandoutPage(form.Value("page"))
	return p.args(), err
}

// storedHandoutPage is a page setup as the lists tables hold it: checked, and
// NULL for the defaults.
func storedHandoutPage(s string) (any, error) {
	p, err := parseHandoutPage(s)
	if err != nil {
		return nil, err
	}
	return p.normalized()
}

// setHandoutPage adds a patch's page setup to p; nil leaves it alone.
func setHandoutPage(p *patch, s *string) error {
	if s == nil {
		return nil
	}
	v, err := storedHandoutPage(*s)
	if err != nil {
		return err
	}
	p.set("handout_page", v)
	return nil
}
