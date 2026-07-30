// Package palette is the suite's single source of colour. It vendors the uchu
// palette (uchu.style) as OKLCH triples in uchu.json and hands out rungs by
// name, so nothing downstream — core.css, the Go swatch enums, the TypeScript
// pickers — writes a hex value by hand.
//
// The point is the numbering. A rung is (variant, hue, 1..9), and "one step
// darker" is arithmetic rather than a fresh trip to a colour picker. That makes
// a theme a WINDOW on the ramps: light and dark name different rungs for the
// same roles, and high contrast is the same roles shifted outward. It also makes
// the ladder testable — see palette_test.go, which asserts monotonicity and WCAG
// contrast the four theme blocks in core.css used to carry on trust alone.
//
//go:generate go run ../cmd/palettegen -css ../assets/core.css -css ../../dope/dope/web/assets/static/styles.css -go sets_gen.go -ts ../../xy/web/ts/palette_gen.ts -ts-sets label
package palette

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

//go:embed uchu.json sets.json
var source embed.FS

// Generated regions in a stylesheet, keyed by name so one generator can fill
// several — the ramps in core.css, the sticker swatches in dope's layer. Both
// palettegen (which writes them) and palette_test.go (which refuses to look for
// stray hexes inside them) key off these, so the two can never disagree about
// where a region ends.
func BeginMarker(region string) string {
	return "/* ---- BEGIN GENERATED " + region + " (go generate ./palette) ---- */"
}

// EndMarker closes any region.
const EndMarker = "/* ---- END GENERATED ---- */"

// OKLCH is one rung: perceptual lightness 0..1, chroma, hue in degrees.
type OKLCH struct {
	L, C, H float64
}

type doc struct {
	Anchors map[string][3]float64              `json:"anchors"`
	Ramps   map[string]map[string][][3]float64 `json:"ramps"`
}

// NamedColor is one entry of a set: the enum token the apps store, and the
// colour it paints.
type NamedColor struct {
	Name string `json:"name"`
	Hex  string `json:"hex"`
}

// setSpec is either a rung sweep (one rung across several hues) or a literal
// list. Literal exists for a set whose values are persisted somewhere and so
// cannot move onto the ramps without a migration.
type setSpec struct {
	Variant string       `json:"variant"`
	Rung    int          `json:"rung"`
	Hues    []string     `json:"hues"`
	Literal []NamedColor `json:"literal"`
}

var (
	loaded doc
	sets   map[string]setSpec
)

func init() {
	b, err := source.ReadFile("uchu.json")
	if err != nil {
		panic("palette: " + err.Error())
	}
	if err := json.Unmarshal(b, &loaded); err != nil {
		panic("palette: uchu.json: " + err.Error())
	}
	s, err := source.ReadFile("sets.json")
	if err != nil {
		panic("palette: " + err.Error())
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(s, &raw); err != nil {
		panic("palette: sets.json: " + err.Error())
	}
	sets = map[string]setSpec{}
	for name, body := range raw {
		if strings.HasPrefix(name, "_") {
			continue
		}
		var spec setSpec
		if err := json.Unmarshal(body, &spec); err != nil {
			panic("palette: sets.json: " + name + ": " + err.Error())
		}
		sets[name] = spec
	}
}

// SetNames lists the declared sets, sorted.
func SetNames() []string {
	out := make([]string, 0, len(sets))
	for n := range sets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Set resolves a named set to its colours. A rung sweep becomes one rung across
// its hues, in the declared order; a literal list is returned as written.
func Set(name string) []NamedColor {
	spec, ok := sets[name]
	if !ok {
		panic("palette: no set " + name)
	}
	if spec.Literal != nil {
		return append([]NamedColor(nil), spec.Literal...)
	}
	out := make([]NamedColor, 0, len(spec.Hues))
	for _, hue := range spec.Hues {
		out = append(out, NamedColor{Name: hue, Hex: Rung(spec.Variant, hue, spec.Rung).Hex()})
	}
	return out
}

// Variants are the two palettes uchu publishes: "base" is the saturated set,
// "pastel" the muted one. They share yang and yin.
func Variants() []string { return []string{"base", "pastel"} }

// Hues lists the ramp names of a variant, sorted.
func Hues(variant string) []string {
	out := make([]string, 0, len(loaded.Ramps[variant]))
	for h := range loaded.Ramps[variant] {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// Anchors are the two rung-less values, yang (paper) and yin (ink).
func Anchors() []string { return []string{"yang", "yin"} }

// Rung returns one rung of a ramp. n is 1..9, light to dark. It panics on an
// unknown name: every caller is either generated code or a test, so a bad rung
// is a build-time mistake and should not be recoverable.
func Rung(variant, hue string, n int) OKLCH {
	ramp, ok := loaded.Ramps[variant][hue]
	if !ok {
		panic(fmt.Sprintf("palette: no ramp %q in variant %q", hue, variant))
	}
	if n < 1 || n > len(ramp) {
		panic(fmt.Sprintf("palette: rung %d out of range for %s-%s", n, variant, hue))
	}
	v := ramp[n-1]
	return OKLCH{v[0], v[1], v[2]}
}

// Anchor returns yang or yin.
func Anchor(name string) OKLCH {
	v, ok := loaded.Anchors[name]
	if !ok {
		panic("palette: no anchor " + name)
	}
	return OKLCH{v[0], v[1], v[2]}
}

// Lookup resolves a token name as written in core.css and in the sets below:
// "yang", "yin", "yin-9", "pastel-green-1". A bare hue-rung means the base
// variant; the "pastel-" prefix selects the muted one.
func Lookup(name string) OKLCH {
	if o, ok := loaded.Anchors[name]; ok {
		return OKLCH{o[0], o[1], o[2]}
	}
	variant := "base"
	if rest, ok := strings.CutPrefix(name, "pastel-"); ok {
		variant, name = "pastel", rest
	}
	i := strings.LastIndex(name, "-")
	if i < 0 {
		panic("palette: cannot parse rung name " + name)
	}
	var n int
	if _, err := fmt.Sscanf(name[i+1:], "%d", &n); err != nil {
		panic("palette: cannot parse rung name " + name)
	}
	return Rung(variant, name[:i], n)
}

// ---- colour maths: OKLCH → sRGB, and WCAG contrast ----------------------

func linToSRGB(c float64) float64 {
	if c <= 0.0031308 {
		return c * 12.92
	}
	return 1.055*math.Pow(c, 1/2.4) - 0.055
}

func srgbToLin(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func clamp(v float64) float64 { return math.Min(1, math.Max(0, v)) }

// RGB converts a rung to sRGB in 0..1, gamut-clipped per channel. Every uchu
// rung is inside sRGB, so the clip is a guard rather than a conversion step.
func (o OKLCH) RGB() (r, g, b float64) {
	a := o.C * math.Cos(o.H*math.Pi/180)
	bb := o.C * math.Sin(o.H*math.Pi/180)

	l := math.Pow(o.L+0.3963377774*a+0.2158037573*bb, 3)
	m := math.Pow(o.L-0.1055613458*a-0.0638541728*bb, 3)
	s := math.Pow(o.L-0.0894841775*a-1.2914855480*bb, 3)

	return clamp(linToSRGB(+4.0767416621*l - 3.3077115913*m + 0.2309699292*s)),
		clamp(linToSRGB(-1.2684380046*l + 2.6097574011*m - 0.3413193965*s)),
		clamp(linToSRGB(-0.0041960863*l - 0.7034186147*m + 1.7076147010*s))
}

// Hex renders a rung as #rrggbb, for the emitters that cannot say oklch():
// the Go swatch enums (compared against stored board data) and the TypeScript
// pickers (which do their own contrast maths on hex).
func (o OKLCH) Hex() string {
	r, g, b := o.RGB()
	return fmt.Sprintf("#%02x%02x%02x", round(r*255), round(g*255), round(b*255))
}

// CSS renders a rung as an oklch() function — what core.css carries, so the
// browser interpolates in the space the palette was designed in.
func (o OKLCH) CSS() string {
	return fmt.Sprintf("oklch(%s %s %s)", num(o.L), num(o.C), num(o.H))
}

// CSSAlpha renders a rung at a given alpha. Used for the transparent twins a
// gradient endpoint needs: a fade must end on the alpha-0 version of its OWN
// colour, or the ramp picks up a tint from whatever `transparent` resolves to.
func (o OKLCH) CSSAlpha(a float64) string {
	return fmt.Sprintf("oklch(%s %s %s / %s)", num(o.L), num(o.C), num(o.H), num(a))
}

func round(f float64) int { return int(math.Round(f)) }

// num trims a float to the shortest form that round-trips, so the generated CSS
// reads like something a person wrote.
func num(f float64) string {
	s := fmt.Sprintf("%.4f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// Luminance is WCAG 2.1 relative luminance.
func (o OKLCH) Luminance() float64 {
	r, g, b := o.RGB()
	return 0.2126*srgbToLin(r) + 0.7152*srgbToLin(g) + 0.0722*srgbToLin(b)
}

// Contrast is the WCAG 2.1 contrast ratio between two rungs, 1..21.
func Contrast(a, b OKLCH) float64 {
	la, lb := a.Luminance(), b.Luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
