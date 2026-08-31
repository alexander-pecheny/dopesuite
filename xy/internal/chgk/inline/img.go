package inline

import (
	"math"
	"strconv"
	"strings"
)

// Img is a parsed (img …) directive: the last whitespace token is the file name,
// the rest are options (chgksuite parseimg).
type Img struct {
	Name   string
	Width  float64 // px; -1 = unset
	Height float64 // px; -1 = unset
	Big    bool
	Inline bool
}

// ParseImg parses the argument of an (img …) directive. ok is false when it names
// no file.
func ParseImg(arg string) (Img, bool) {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return Img{}, false
	}
	im := Img{Name: fields[len(fields)-1], Width: -1, Height: -1}
	for _, o := range fields[:len(fields)-1] {
		switch {
		case o == "big":
			im.Big = true
		case o == "inline":
			im.Inline = true
		case strings.HasPrefix(o, "w="):
			im.Width = parseSingleSize(o[2:])
		case strings.HasPrefix(o, "h="):
			im.Height = parseSingleSize(o[2:])
		case strings.HasPrefix(o, "inline="):
			v := o[len("inline="):]
			im.Inline = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
		}
	}
	return im, true
}

// SizeInches computes the rendered size of an image in inches, mirroring
// chgksuite's parseimg + python-docx add_picture (px at 120 dpi;
// proportional_resize when neither dimension is given). native{W,H} are the
// image's pixel dimensions.
//
// Both exporters go through this, so a picture is the same size in the .docx and
// in the PDF.
func (im Img) SizeInches(nativeW, nativeH int) (w, h float64) {
	if nativeW <= 0 || nativeH <= 0 {
		nativeW, nativeH = 1, 1
	}
	if im.Inline {
		h = 1.0 / 6 // one line tall
		return h * float64(nativeW) / float64(nativeH), h
	}
	rw, rh := proportionalResize(nativeW, nativeH)
	width, height := im.Width, im.Height
	if width == -1 && height == -1 {
		return float64(rw) / 120, float64(rh) / 120
	}
	if width != -1 && height == -1 {
		height = float64(rh) * (width / float64(rw))
	} else if width == -1 && height != -1 {
		width = float64(rw) * (height / float64(rh))
	}
	return width / 120, height / 120
}

// SizePixels is parseimg(dimensions="pixels"), which is what the HTML exports
// ask for: the picture's own size when the directive names none, and the
// missing dimension derived from the other otherwise. native is true when
// neither was named, which is when chgksuite's numbers are whole — with an
// option they have all been through float().
func (im Img) SizePixels(nativeW, nativeH int) (w, h float64, native bool) {
	rw, rh := proportionalResize(max(nativeW, 1), max(nativeH, 1))
	if im.Width == -1 && im.Height == -1 {
		return float64(rw), float64(rh), true
	}
	w, h = im.Width, im.Height
	if w != -1 && h == -1 {
		h = float64(rh) * (w / float64(rw))
	} else if w == -1 && h != -1 {
		w = float64(rw) * (h / float64(rh))
	}
	return w, h, false
}

// proportionalResize mirrors chgksuite: clamp the longest side into [200, 600] px.
func proportionalResize(w, h int) (int, int) {
	mx := max(w, h)
	if mx > 600 {
		return w * 600 / mx, h * 600 / mx
	}
	if mx < 200 {
		return w * 200 / mx, h * 200 / mx
	}
	return w, h
}

// parseSingleSize mirrors chgksuite parse_single_size (px default; in→×120; em→×25).
func parseSingleSize(s string) float64 {
	switch {
	case strings.HasSuffix(s, "in"):
		v, _ := strconv.ParseFloat(s[:len(s)-2], 64)
		return v * 120
	case strings.HasSuffix(s, "em"):
		v, _ := strconv.ParseFloat(s[:len(s)-2], 64)
		return v * 25
	case strings.HasSuffix(s, "px"):
		s = s[:len(s)-2]
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// Round2 rounds to two decimals (used when emitting lengths).
func Round2(f float64) float64 { return math.Round(f*100) / 100 }
