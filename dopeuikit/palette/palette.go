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
//go:generate go run ../cmd/palettegen -css ../assets/core.css -css ../../dope/dope/web/assets/static/styles.css -css ../../xy/web/assets/static/styles.css -go sets_gen.go -ts ../../xy/web/ts/palette_gen.ts -ts-sets label
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

// setSpec is either a themed set (a name per colour, and a rung chosen per
// theme so the name survives a theme switch) or a literal list. Literal exists
// for a set whose values are persisted somewhere and so cannot move onto the
// ramps without a migration.
type setSpec struct {
	Themes  map[string]themeSpec `json:"themes"`
	Tones   []toneSpec           `json:"tones"`
	Colors  []colorSpec          `json:"colors"`
	Literal []NamedColor         `json:"literal"`
}

// toneSpec is a register the whole set is offered in. The first is the rung the
// theme's floor picks; a later one takes that rung and removes `darken` of its
// LIGHTNESS, holding hue and chroma. Not a rung further down the ramp: uchu's
// yellow bottoms out at L 0.62 and its brown lower still, so an offset leaves
// those two hues with no second register at all, while scaling gives every hue
// one. A dark theme's card is a MID grey, not black, so a deep colour still
// reads on it — by chroma, where WCAG only measures luminance.
type toneSpec struct {
	Suffix string  `json:"suffix"`
	Darken float64 `json:"darken"`
}

// themeSpec is the ground a theme composes this set on, and the contrast the
// set must clear against it. The rung follows from the two.
type themeSpec struct {
	Ground string  `json:"ground"`
	Floor  float64 `json:"floor"`
}

// colorSpec is one entry: a name and the ramp it reads. `ramp` names a vendored
// uchu hue; `mix` and `desat` DERIVE one, which is how the set offers hues uchu
// does not ship (teal between green and blue, brown as a drained orange). Both
// are arithmetic on the vendored ramps rather than a fresh hex, so a derived
// hue is still a ladder with nine rungs on it.
//
// bias pushes a theme's pick that many rungs deeper than the rule would. Brown
// is the one colour whose identity IS its darkness: the rule takes the most
// chromatic rung that clears the ground, and for a drained orange that lands on
// a tan. Every other entry leaves this empty.
//
// single opts a colour out of every register but the first. Grey is the one that
// needs it: a deeper tone of a colour with no chroma is separated from the card
// by lightness alone, and the dark theme's card sits in the middle of the ramp,
// so "deep grey" lands on the card itself.
type colorSpec struct {
	Name   string         `json:"name"`
	Ramp   string         `json:"ramp"`
	Mix    []any          `json:"mix"`
	Desat  []any          `json:"desat"`
	Bias   map[string]int `json:"bias"`
	Single bool           `json:"single"`
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

// Names lists a set's colour names, in offer order. This is what an app STORES:
// a themed set's hex is a rendering of the name, and differs per theme.
func Names(set string) []string {
	spec := mustSet(set)
	if spec.Literal != nil {
		out := make([]string, 0, len(spec.Literal))
		for _, c := range spec.Literal {
			out = append(out, c.Name)
		}
		return out
	}
	out := make([]string, 0, len(spec.Colors)*len(spec.Tones))
	for i, t := range spec.Tones {
		for _, c := range spec.Colors {
			if i > 0 && c.Single {
				continue
			}
			out = append(out, c.Name+t.Suffix)
		}
	}
	return out
}

// Tone reports how much lightness a name gives up against the floor's own pick:
// 0 for the set's default register, more for a deeper one. It is what tells a
// caller which entries carry the contrast guarantee and which are the second
// register.
func Tone(set, name string) float64 {
	spec := mustSet(set)
	drop, found := 0.0, false
	for _, t := range spec.Tones {
		if t.Suffix == "" || !strings.HasSuffix(name, t.Suffix) {
			continue
		}
		if !found || t.Darken > drop {
			drop, found = t.Darken, true
		}
	}
	return drop
}

// Themes lists a set's themes, sorted. Empty for a literal set, which paints
// the same colour whatever the page is doing.
func Themes(set string) []string {
	out := make([]string, 0, len(mustSet(set).Themes))
	for t := range mustSet(set).Themes {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Set resolves a named set as one theme sees it: for each colour, the most
// chromatic rung of its ramp that still clears the theme's floor against the
// theme's ground. A literal set ignores the theme and is returned as written.
//
// This is the whole idea. No hex clears 3:1 against BOTH a near-white card and
// a mid-grey one — the two bands do not overlap — so a colour that survives a
// theme switch cannot be a hex. It has to be a name with a rung per theme, and
// the rung is then arithmetic rather than a choice.
func Set(name, theme string) []NamedColor {
	if mustSet(name).Literal != nil {
		return append([]NamedColor(nil), mustSet(name).Literal...)
	}
	names, rungs := Names(name), Rungs(name, theme)
	out := make([]NamedColor, len(names))
	for i := range names {
		out[i] = NamedColor{Name: names[i], Hex: rungs[i].Hex()}
	}
	return out
}

// Rungs is Set's own source: the rung each colour resolves to under a theme,
// for callers that need the colour itself rather than its hex.
func Rungs(name, theme string) []OKLCH {
	spec := mustSet(name)
	th, ok := spec.Themes[theme]
	if !ok {
		panic(fmt.Sprintf("palette: set %q has no theme %q", name, theme))
	}
	ground := Lookup(th.Ground)
	out := make([]OKLCH, 0, len(spec.Colors)*len(spec.Tones))
	for i, t := range spec.Tones {
		for _, c := range spec.Colors {
			if i > 0 && c.Single {
				continue
			}
			r := pickRung(c.ramp(), ground, th.Floor, c.Bias[theme])
			out = append(out, fitGamut(OKLCH{r.L * (1 - t.Darken), r.C, r.H}))
		}
	}
	return out
}

// Ink is the ramp end that reads on a set colour — uchu's own paper or ink,
// whichever is further from it. A label's colour is the user's to choose, so
// the text on it has to follow the colour rather than be fixed.
func Ink(fill OKLCH) OKLCH {
	yin, yang := Anchor("yin"), Anchor("yang")
	if Contrast(fill, yin) >= Contrast(fill, yang) {
		return yin
	}
	return yang
}

func mustSet(name string) setSpec {
	spec, ok := sets[name]
	if !ok {
		panic("palette: no set " + name)
	}
	return spec
}

// pickRung takes the most chromatic rung that clears the floor, then steps
// `bias` further down the ladder. Most chromatic, not nearest some target
// lightness: the floor already decides how light the rung may be, and among the
// rungs it allows the useful one is the one that says its hue loudest.
func pickRung(ramp []OKLCH, ground OKLCH, floor float64, bias int) OKLCH {
	best := -1
	for i, r := range ramp {
		if Contrast(r, ground) >= floor && (best < 0 || r.C > ramp[best].C) {
			best = i
		}
	}
	if best < 0 {
		panic(fmt.Sprintf("palette: no rung clears %.1f:1 against %s", floor, ground.Hex()))
	}
	return ramp[min(best+bias, len(ramp)-1)]
}

// ramp materialises a colour's nine rungs: a vendored hue, or one derived from
// vendored hues. Derivation interpolates in OKLCH — the space the ramps are
// published in — and then pulls the result back into sRGB, since a hue uchu
// never drew may ask for a chroma the screen cannot show.
func (c colorSpec) ramp() []OKLCH {
	switch {
	case c.Ramp != "":
		return baseRamp(c.Ramp)
	case len(c.Mix) == 3:
		a, b := baseRamp(c.Mix[0].(string)), baseRamp(c.Mix[1].(string))
		t := c.Mix[2].(float64)
		out := make([]OKLCH, len(a))
		for i := range a {
			out[i] = fitGamut(OKLCH{
				L: a[i].L + (b[i].L-a[i].L)*t,
				C: a[i].C + (b[i].C-a[i].C)*t,
				H: lerpHue(a[i].H, b[i].H, t),
			})
		}
		return out
	case len(c.Desat) == 2:
		a, k := baseRamp(c.Desat[0].(string)), c.Desat[1].(float64)
		out := make([]OKLCH, len(a))
		for i := range a {
			out[i] = OKLCH{a[i].L, a[i].C * k, a[i].H}
		}
		return out
	}
	panic("palette: colour " + c.Name + " names no ramp")
}

func baseRamp(hue string) []OKLCH {
	out := make([]OKLCH, 9)
	for i := range out {
		out[i] = Rung("base", hue, i+1)
	}
	return out
}

// lerpHue walks the SHORT way round the wheel. Green to blue the long way is
// through red, which is not a colour between them.
func lerpHue(a, b, t float64) float64 {
	d := math.Mod(b-a+540, 360) - 180
	return math.Mod(a+d*t+360, 360)
}

// fitGamut drops chroma until the rung fits sRGB, holding lightness and hue —
// the same compromise a browser makes for an out-of-gamut oklch(). Without it a
// derived teal is clipped per channel, which shifts its hue and drains it grey.
func fitGamut(o OKLCH) OKLCH {
	if o.inGamut() {
		return o
	}
	lo, hi := 0.0, o.C
	for range 40 {
		mid := (lo + hi) / 2
		if (OKLCH{o.L, mid, o.H}).inGamut() {
			lo = mid
		} else {
			hi = mid
		}
	}
	return OKLCH{o.L, lo, o.H}
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

// linearRGB converts a rung to linear sRGB, unclamped — a channel outside 0..1
// means the rung is outside the screen's gamut, which is what fitGamut reads.
func (o OKLCH) linearRGB() (r, g, b float64) {
	a := o.C * math.Cos(o.H*math.Pi/180)
	bb := o.C * math.Sin(o.H*math.Pi/180)

	l := math.Pow(o.L+0.3963377774*a+0.2158037573*bb, 3)
	m := math.Pow(o.L-0.1055613458*a-0.0638541728*bb, 3)
	s := math.Pow(o.L-0.0894841775*a-1.2914855480*bb, 3)

	return +4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s
}

// Lab is the rung's position on the OKLab colour plane — chroma and hue as a
// pair of coordinates, with lightness left out. It is what "which of these
// colours is this one nearest?" is asked in, since two rungs of the same hue are
// the same colour at different lightnesses.
func (o OKLCH) Lab() (a, b float64) {
	r := o.H * math.Pi / 180
	return o.C * math.Cos(r), o.C * math.Sin(r)
}

func (o OKLCH) inGamut() bool {
	const eps = 1e-4
	r, g, b := o.linearRGB()
	for _, v := range [3]float64{r, g, b} {
		if v < -eps || v > 1+eps {
			return false
		}
	}
	return true
}

// RGB converts a rung to sRGB in 0..1, gamut-clipped per channel. Every uchu
// rung is inside sRGB, so the clip is a guard rather than a conversion step.
func (o OKLCH) RGB() (r, g, b float64) {
	lr, lg, lb := o.linearRGB()
	return clamp(linToSRGB(lr)), clamp(linToSRGB(lg)), clamp(linToSRGB(lb))
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

// Distance is how far apart two rungs are on the OKLab solid — lightness AND
// chroma, in a space built so that equal steps look equal. It is the honest
// measure for "can you see this dot on that card", which WCAG's ratio is not:
// that ratio reads luminance alone, so it calls a saturated purple on a mid
// grey invisible when it plainly is not.
func Distance(a, b OKLCH) float64 {
	aa, ab := a.Lab()
	ba, bb := b.Lab()
	return math.Sqrt((a.L-b.L)*(a.L-b.L) + (aa-ba)*(aa-ba) + (ab-bb)*(ab-bb))
}

// Contrast is the WCAG 2.1 contrast ratio between two rungs, 1..21.
func Contrast(a, b OKLCH) float64 {
	la, lb := a.Luminance(), b.Luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
