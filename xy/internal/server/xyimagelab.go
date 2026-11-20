package server

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/handout"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/xyimage"
)

// The Edited Image prototype (ADR-0024): /lab/xyimage lays text out in the
// browser, freezes the lines into SVG overlays, and posts the frame's layers
// here in stacking order: pictures, and between them runs of black boxes and
// text as vector overlays. The server only places them, on a typst page the
// size of the frame. Nothing is laid out here, which is the point being tested.
// Nothing is stored either; this is a lab page, not the feature.

const (
	xyimageTimeout  = 60 * time.Second
	xyimageMaxBytes = 64 << 20
	// xyimageMaxMM bounds a frame side; an A0 sheet is 1189 mm.
	xyimageMaxMM     = 2000
	xyimageMaxLayers = 200
	// xyimageFarMM bounds how far a picture may sit or reach past the frame.
	xyimageFarMM = 8 * xyimageMaxMM
	// xyimageMinMM is the smallest side a picture may have.
	xyimageMinMM = 0.1
	// mmDecimals is how finely the typst source states a length.
	mmDecimals = 3
)

var (
	xyimageColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	xyimageFile   = regexp.MustCompile(`^f[0-9]{1,3}$`)
)

// xyimageLayer is one layer, in millimetres from the frame's top-left. A
// picture carries its rectangle; an overlay covers the whole frame.
type xyimageLayer struct {
	Kind string  `json:"kind"` // "image" or "svg"
	File string  `json:"file"`
	Ext  string  `json:"ext"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

// xyimageLayout is what the browser sends.
type xyimageLayout struct {
	W      float64        `json:"w"`
	H      float64        `json:"h"`
	BG     string         `json:"bg"`
	Layers []xyimageLayer `json:"layers"`
	// Fonts are the uploaded Faces the overlays use, found in the "font" parts.
	Fonts []xyimage.Face `json:"fonts"`
}

func finiteIn(v, lo, hi float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi
}

func (l xyimageLayer) valid() bool {
	if !xyimageFile.MatchString(l.File) {
		return false
	}
	switch l.Kind {
	case "svg":
		return true
	case "image":
		return (l.Ext == "png" || l.Ext == "jpg") &&
			finiteIn(l.X, -xyimageFarMM, xyimageFarMM) && finiteIn(l.Y, -xyimageFarMM, xyimageFarMM) &&
			finiteIn(l.W, xyimageMinMM, xyimageFarMM) && finiteIn(l.H, xyimageMinMM, xyimageFarMM)
	}
	return false
}

func (l xyimageLayout) valid() bool {
	if !finiteIn(l.W, 1, xyimageMaxMM) || !finiteIn(l.H, 1, xyimageMaxMM) ||
		!xyimageColour.MatchString(l.BG) || len(l.Layers) > xyimageMaxLayers {
		return false
	}
	for _, ly := range l.Layers {
		if !ly.valid() {
			return false
		}
	}
	return true
}

// fileName is the name typst reads the layer's bytes under.
func (l xyimageLayer) fileName() string {
	if l.Kind == "svg" {
		return l.File + ".svg"
	}
	return l.File + "." + l.Ext
}

func mm(v float64) string { return strconv.FormatFloat(v, 'f', mmDecimals, floatBits) + "mm" }

// xyimageTyp is the whole typst document: a page the size of the frame and
// every layer placed in order; the page edge clips whatever overhangs it. A
// picture gets both sides and fit "stretch", because given only a width typst
// re-centres an image taller than the page instead of pinning its top-left.
func xyimageTyp(l xyimageLayout) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#set page(width: %s, height: %s, margin: 0pt, fill: rgb(%q))\n", mm(l.W), mm(l.H), l.BG)
	for _, ly := range l.Layers {
		if ly.Kind == "svg" {
			fmt.Fprintf(&b, "#place(top + left, image(%q, width: %s, height: %s))\n", ly.fileName(), mm(l.W), mm(l.H))
			continue
		}
		fmt.Fprintf(&b, "#place(top + left, dx: %s, dy: %s, image(%q, width: %s, height: %s, fit: \"stretch\"))\n",
			mm(ly.X), mm(ly.Y), ly.fileName(), mm(ly.W), mm(ly.H))
	}
	return b.String()
}

// readXyimage reads the layout and every layer's bytes off the request.
func readXyimage(w http.ResponseWriter, r *http.Request) (xyimageLayout, map[string][]byte, bool) {
	var l xyimageLayout
	r.Body = http.MaxBytesReader(w, r.Body, xyimageMaxBytes)
	form, err := readMultipart(r, xyimageMaxBytes)
	if err != nil {
		handleUser(w, err)
		return l, nil, false
	}
	if err := json.Unmarshal([]byte(form.Value("layout")), &l); err != nil || !l.valid() {
		httpError(w, http.StatusBadRequest, "bad layout")
		return l, nil, false
	}
	images := map[string][]byte{}
	for _, ly := range l.Layers {
		f := form.Files(ly.File)
		if len(f) != 1 {
			httpError(w, http.StatusBadRequest, "missing layer "+ly.File)
			return l, nil, false
		}
		images[ly.fileName()] = f[0].Data
	}
	faces, err := (&xyimage.File{Bake: xyimage.Bake{Fonts: l.Fonts}}).Faces(form.Images())
	if err != nil {
		handleUser(w, xyimage.UserError("xyimage", err))
		return l, nil, false
	}
	maps.Copy(images, faces)
	return l, images, true
}

func (s *server) handleXyimageRender(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	l, images, ok := readXyimage(w, r)
	if !ok {
		return
	}
	ts, ok := s.handoutTypesetter(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), xyimageTimeout)
	defer cancel()
	if err := ts.SetImages(ctx, images); err != nil {
		renderFailed(w, ctx, "xyimage images", err)
		return
	}
	// PDF only. typst's SVG output embeds an SVG image verbatim rather than
	// setting its text, so it cannot stand in for the PDF in a comparison.
	pdf, _, err := ts.Compile(ctx, xyimageTyp(l), true)
	if err != nil {
		renderFailed(w, ctx, "xyimage pdf", err)
		return
	}
	serveHandout(w, pdf, "xyimage.pdf", "application/pdf")
}

// handleXyimageFont serves the exact Noto Sans files typst sets text in, so
// the browser lays the text out against the same glyphs.
func (s *server) handleXyimageFont(w http.ResponseWriter, r *http.Request) {
	b, ok := handout.BundledFontFile(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "font/ttf")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(b)
}
