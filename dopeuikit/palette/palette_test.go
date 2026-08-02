package palette

import (
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const corePath = "../assets/core.css"

// The four theme blocks, in cascade order: each layers over the ones before it,
// exactly as the browser resolves them.
var themes = []struct {
	name     string
	selector []string
}{
	{"light", []string{":root"}},
	{"dark", []string{":root", `:root[data-theme="dark"]`}},
	{"light+contrast", []string{":root", `:root[data-theme="light"][data-contrast="high"]`}},
	{"dark+contrast", []string{":root", `:root[data-theme="dark"]`, `:root[data-theme="dark"][data-contrast="high"]`}},
}

var (
	declRe    = regexp.MustCompile(`--([a-z0-9-]+)\s*:\s*([^;{}]+);`)
	varRefRe  = regexp.MustCompile(`var\(\s*--([a-z0-9-]+)\s*\)`)
	commentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	hexRe     = regexp.MustCompile(`#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})\b`)
	rungRefRe = regexp.MustCompile(`^var\(--uchu-([a-z0-9-]+)\)$`)
	derivedRe = regexp.MustCompile(`^oklch\(from `)
	// A rung wearing a theme's cast: lightness from the rung, chroma and hue
	// replaced. Opaque, so it stays on the ladder and must keep being checked —
	// matching this BEFORE derivedRe is what keeps dark inside every assertion
	// below rather than silently skipped as a wash.
	tintedRe = regexp.MustCompile(`^oklch\(from var\(--([a-z0-9-]+)\) l (\S+) (\S+)\)$`)
	mixRe    = regexp.MustCompile(`^color-mix\(in oklab,\s*var\(--([a-z0-9-]+)\)\s+([\d.]+)%,\s*var\(--([a-z0-9-]+)\)\s*\)$`)
	// The generated ramps themselves: `oklch(L C H)` with no alpha and no `from`.
	oklchRe = regexp.MustCompile(`^oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)$`)
)

// mix interpolates two colours in OKLab, which is what the browser does for
// `color-mix(in oklab, …)`. The washes are defined that way so each one sits on
// its own theme's paper, so the test has to follow them there.
func mix(a OKLCH, frac float64, b OKLCH) OKLCH {
	toLab := func(c OKLCH) (l, x, y float64) {
		return c.L, c.C * math.Cos(c.H*math.Pi/180), c.C * math.Sin(c.H*math.Pi/180)
	}
	al, ax, ay := toLab(a)
	bl, bx, by := toLab(b)
	l := al*frac + bl*(1-frac)
	x := ax*frac + bx*(1-frac)
	y := ay*frac + by*(1-frac)
	h := math.Atan2(y, x) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	return OKLCH{l, math.Hypot(x, y), h}
}

func core(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read %s: %v", corePath, err)
	}
	return string(b)
}

// block returns the body of a top-level rule with the given selector.
func block(t *testing.T, css, selector string) string {
	t.Helper()
	// Anchor on a line start so `:root` does not match `:root[data-theme=...]`.
	idx := strings.Index(css, "\n"+selector+" {")
	if idx < 0 {
		t.Fatalf("selector %q not found in core.css", selector)
	}
	open := strings.Index(css[idx:], "{") + idx
	depth := 0
	for i := open; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[open+1 : i]
			}
		}
	}
	t.Fatalf("unterminated block for %q", selector)
	return ""
}

// tokensFor layers the selectors of a theme into one name→value map.
func tokensFor(t *testing.T, css string, selectors []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, sel := range selectors {
		body := commentRe.ReplaceAllString(block(t, css, sel), "")
		for _, m := range declRe.FindAllStringSubmatch(body, -1) {
			out[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return out
}

// resolve follows a role token's alias chain to a concrete colour. It reports
// derived=true for the alpha washes (`oklch(from …)`), which have no place on
// the opaque ladder and are checked separately.
func resolve(tokens map[string]string, name string) (c OKLCH, derived, ok bool) {
	seen := map[string]bool{}
	for range 16 {
		if seen[name] {
			return OKLCH{}, false, false
		}
		seen[name] = true
		v, present := tokens[name]
		if !present {
			return OKLCH{}, false, false
		}
		if m := rungRefRe.FindStringSubmatch(v); m != nil {
			return Lookup(m[1]), false, true
		}
		if m := oklchRe.FindStringSubmatch(v); m != nil {
			l, _ := strconv.ParseFloat(m[1], 64)
			c, _ := strconv.ParseFloat(m[2], 64)
			h, _ := strconv.ParseFloat(m[3], 64)
			return OKLCH{l, c, h}, false, true
		}
		if m := tintedRe.FindStringSubmatch(v); m != nil {
			base, _, ok := resolve(tokens, m[1])
			c, okC := number(tokens, m[2])
			h, okH := number(tokens, m[3])
			if !ok || !okC || !okH {
				return OKLCH{}, false, false
			}
			return OKLCH{base.L, c, h}, false, true
		}
		if derivedRe.MatchString(v) {
			return OKLCH{}, true, true
		}
		if m := mixRe.FindStringSubmatch(v); m != nil {
			base, _, okA := resolve(tokens, m[1])
			hue, _, okB := resolve(tokens, m[3])
			if !okA || !okB {
				return OKLCH{}, false, false
			}
			frac, err := strconv.ParseFloat(m[2], 64)
			if err != nil {
				return OKLCH{}, false, false
			}
			return mix(base, frac/100, hue), false, true
		}
		if m := hexRe.FindStringSubmatch(v); m != nil && strings.HasPrefix(v, "#") {
			return fromHex(m[1]), false, true
		}
		m := varRefRe.FindStringSubmatch(v)
		if m == nil {
			return OKLCH{}, false, false
		}
		name = m[1]
	}
	return OKLCH{}, false, false
}

// number reads a scalar token — the cast's chroma and hue — following var()
// chains the same way a colour would be followed.
func number(tokens map[string]string, v string) (float64, bool) {
	for range 8 {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
		m := varRefRe.FindStringSubmatch(v)
		if m == nil {
			return 0, false
		}
		next, ok := tokens[m[1]]
		if !ok {
			return 0, false
		}
		v = strings.TrimSpace(next)
	}
	return 0, false
}

func fromHex(h string) OKLCH {
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	v, _ := strconv.ParseUint(h, 16, 32)
	r := float64((v>>16)&0xff) / 255
	g := float64((v>>8)&0xff) / 255
	b := float64(v&0xff) / 255
	return toOKLCH(r, g, b)
}

func toOKLCH(r, g, b float64) OKLCH {
	lr, lg, lb := srgbToLin(r), srgbToLin(g), srgbToLin(b)
	l := math.Cbrt(0.4122214708*lr + 0.5363325363*lg + 0.0514459929*lb)
	m := math.Cbrt(0.2119034982*lr + 0.6806995451*lg + 0.1073969566*lb)
	s := math.Cbrt(0.0883024619*lr + 0.2817188376*lg + 0.6299787005*lb)
	L := 0.2104542553*l + 0.7936177850*m - 0.0040720468*s
	A := 1.9779984951*l - 2.4285922050*m + 0.4505937099*s
	B := 0.0259040371*l + 0.7827717662*m - 0.8086757660*s
	h := math.Atan2(B, A) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	return OKLCH{L, math.Hypot(A, B), h}
}

// ── the ladder ────────────────────────────────────────────────────────────

// neutralRoles are the surfaces, lines and inks — everything the ladder owns.
// Each must name a --uchu-* rung in every theme that declares it, directly or
// through an alias, and never a hex. Hand-picked hexes are how the old palette
// accumulated 73 neutrals across four themes with some pairs 0.003 apart.
var neutralRoles = []string{
	"paper", "surface", "page", "structure", "surface-tint", "hover-bg",
	"action-bg", "action-hover-bg", "field-bg", "action-disabled-bg",
	"grid", "grid-dark", "text", "text-strong", "muted", "muted-light", "shade",
}

func TestNeutralRolesNameARung(t *testing.T) {
	css := core(t)
	for _, th := range themes {
		t.Run(th.name, func(t *testing.T) {
			tokens := tokensFor(t, css, th.selector)
			for _, role := range neutralRoles {
				v, ok := tokens[role]
				if !ok {
					continue // this theme inherits it
				}
				c, derived, resolved := resolve(tokens, role)
				if derived {
					continue // an alpha wash, checked by the ladder tests instead
				}
				if !resolved {
					t.Errorf("--%s = %q does not resolve to a colour", role, v)
					continue
				}
				// The ladder owns LIGHTNESS; a theme's cast may replace chroma
				// and hue (see --tint-c/--tint-h in the dark block). So the
				// invariant is that the role's lightness is a rung's lightness —
				// which a rung reference gives for free and a hand-picked hex
				// only lands on by luck.
				if !onARung(c.L) {
					t.Errorf("--%s resolves to L=%.4f, which is no rung's lightness — name a --uchu-* rung instead of a literal",
						role, c.L)
				}
			}
		})
	}
}

// onARung reports whether a lightness is exactly some vendored rung's.
func onARung(l float64) bool {
	const eps = 1e-9
	for _, name := range Anchors() {
		if math.Abs(Anchor(name).L-l) < eps {
			return true
		}
	}
	for _, variant := range Variants() {
		for _, hue := range Hues(variant) {
			for n := 1; n <= 9; n++ {
				if math.Abs(Rung(variant, hue, n).L-l) < eps {
					return true
				}
			}
		}
	}
	return false
}

// TestEveryVarReferenceIsDefined catches a role that points at a token no theme
// declares — a silently unstyled property, and the failure mode that let --header
// and --toolbar sit in the dark high-contrast block styling nothing at all.
func TestEveryVarReferenceIsDefined(t *testing.T) {
	css := commentRe.ReplaceAllString(core(t), "")
	defined := map[string]bool{}
	for _, m := range declRe.FindAllStringSubmatch(css, -1) {
		defined[m[1]] = true
	}
	// Locally scoped properties: set on a component, read in its own subtree.
	local := map[string]bool{"scroll-fade-bg": true, "swatch": true, "cell-fill": true}

	missing := map[string]bool{}
	for _, m := range varRefRe.FindAllStringSubmatch(css, -1) {
		if !defined[m[1]] && !local[m[1]] {
			missing[m[1]] = true
		}
	}
	if len(missing) > 0 {
		var names []string
		for n := range missing {
			names = append(names, "--"+n)
		}
		sort.Strings(names)
		t.Errorf("core.css references %d undefined custom propert(ies): %s",
			len(names), strings.Join(names, ", "))
	}
}

// surfaceLadder walks OUT from paper: the card surface, the tint inset into it,
// the control fill on top, and that control's hover. Every theme must step in
// one direction, so "lifted" means the same thing everywhere.
//
// --page is deliberately absent. In light it aliases --structure; in dark it is
// the darkest value of all, below --surface. It is the backdrop, not a rung of
// the elevation ladder, and folding it in was what made an earlier version of
// this test fail against a correct palette.
var surfaceLadder = []string{"surface", "structure", "action-bg", "action-hover-bg"}

func TestSurfaceLadderIsMonotone(t *testing.T) {
	css := core(t)
	for _, th := range themes {
		t.Run(th.name, func(t *testing.T) {
			steps := ladder(t, css, th.selector)
			// Light descends (paper is brightest); dark ascends.
			descending := steps[0].L > steps[len(steps)-1].L
			for i := 1; i < len(steps); i++ {
				d := steps[i].L - steps[i-1].L
				if descending {
					d = -d
				}
				if d <= 0 {
					t.Errorf("ladder reverses: --%s (L %.3f) then --%s (L %.3f)",
						steps[i-1].name, steps[i-1].L, steps[i].name, steps[i].L)
				}
			}
		})
	}
}

type step struct {
	name string
	L    float64
}

func ladder(t *testing.T, css string, selectors []string) []step {
	t.Helper()
	tokens := tokensFor(t, css, selectors)
	var steps []step
	for _, role := range surfaceLadder {
		c, derived, ok := resolve(tokens, role)
		if !ok || derived {
			t.Fatalf("--%s does not resolve to an opaque colour", role)
		}
		steps = append(steps, step{role, c.L})
	}
	return steps
}

// minGap is the tightest adjacent step in a theme's ladder.
func minGap(steps []step) float64 {
	m := math.Inf(1)
	for i := 1; i < len(steps); i++ {
		m = math.Min(m, math.Abs(steps[i].L-steps[i-1].L))
	}
	return m
}

// TestAdjacentSurfacesAreDistinguishable puts a number on the finding that
// started this work: below about ΔL 0.025 two large flat fills read as one
// colour. Rungs cannot land that close, so this holds by construction — which is
// the point of asserting it, since the previous palette had eleven near-white
// surfaces inside a single 0.10 band.
func TestAdjacentSurfacesAreDistinguishable(t *testing.T) {
	const minGap = 0.03
	css := core(t)
	for _, th := range themes {
		t.Run(th.name, func(t *testing.T) {
			tokens := tokensFor(t, css, th.selector)
			seen := map[string]float64{}
			for _, role := range append([]string{"grid", "grid-dark"}, surfaceLadder...) {
				c, derived, ok := resolve(tokens, role)
				if !ok || derived {
					continue
				}
				for other, L := range seen {
					if d := math.Abs(L - c.L); d > 0 && d < minGap {
						t.Errorf("--%s (L %.3f) and --%s (L %.3f) differ by %.3f — under ΔL %.2f they read as one colour; share a rung or move one",
							role, c.L, other, L, d, minGap)
					}
				}
				seen[role] = c.L
			}
		})
	}
}

// washes are the three things a tinted background can say. They must read as
// ONE tier — a set of equals — which is exactly what the seven they replace did
// not do: those spread from L 0.929 to 0.982 and interleaved with the grays, so
// which wash you were looking at carried no information.
var washes = []string{"wash-positive", "wash-negative", "wash-emphasis"}

func TestWashesAreOneTier(t *testing.T) {
	const spread = 0.05
	css := core(t)
	for _, th := range themes {
		t.Run(th.name, func(t *testing.T) {
			tokens := tokensFor(t, css, th.selector)
			lo, hi := math.Inf(1), math.Inf(-1)
			for _, w := range washes {
				c, _, ok := resolve(tokens, w)
				if !ok {
					t.Fatalf("--%s does not resolve", w)
				}
				lo, hi = math.Min(lo, c.L), math.Max(hi, c.L)

				// A wash is a background, so the body ink has to survive it.
				ink, _, _ := resolve(tokens, "text")
				if got := Contrast(ink, c); got < 7 {
					t.Errorf("--text on --%s is %.2f:1, want >= 7:1", w, got)
				}
				// And it has to be visible against the paper it sits on.
				surface, _, _ := resolve(tokens, "surface")
				if math.Abs(surface.L-c.L) < 0.02 && c.C < 0.02 {
					t.Errorf("--%s is indistinguishable from --surface", w)
				}
			}
			if hi-lo > spread {
				t.Errorf("washes span %.3f lightness, want <= %.2f — they should read as equals", hi-lo, spread)
			}
		})
	}
}

// TestSetsAreOfferable guards what a picker assumes about a set it renders: the
// entries are distinct, and each one can carry ink. The label picker paints its
// own text by choosing yin or yang per fill, so every entry must clear AA
// against at least one of them — otherwise a Label exists that cannot show its
// own name.
func TestSetsAreOfferable(t *testing.T) {
	yin, yang := Anchor("yin"), Anchor("yang")
	for _, name := range SetNames() {
		themes := Themes(name)
		if len(themes) == 0 {
			themes = []string{""}
		}
		for _, theme := range themes {
			t.Run(strings.TrimSuffix(name+"/"+theme, "/"), func(t *testing.T) {
				seen := map[string]string{}
				for _, c := range Set(name, theme) {
					if prev, dup := seen[c.Hex]; dup {
						t.Errorf("%q and %q are both %s — a picker would show one twice", prev, c.Name, c.Hex)
					}
					seen[c.Hex] = c.Name

					fill := fromHex(strings.TrimPrefix(c.Hex, "#"))
					best := math.Max(Contrast(fill, yin), Contrast(fill, yang))
					if best < 4.5 {
						t.Errorf("%s (%s) reaches only %.2f:1 against uchu's ink and paper — nothing can be written on it",
							c.Name, c.Hex, best)
					}
				}
			})
		}
	}
}

// TestLabelSetStandsOffItsCard is the promise the label palette makes, and the
// reason a label stores a name rather than a hex: whatever the reader's theme,
// the dot on a card is a dot you can see. No single colour manages that on both
// — the band that clears 3:1 on a near-white card and the band that clears it on
// a mid-grey one do not overlap — so the rung is the theme's to choose, and this
// asserts it chose one that works. The ground is read out of core.css rather
// than restated here, so moving a surface moves the test with it.
//
// Two registers, two measures. The default one owes WCAG's 3:1 and gets it. The
// -deep one is three rungs below that on purpose, and what keeps it visible is
// CHROMA — a saturated purple on a mid grey reads perfectly well while WCAG,
// which weighs luminance alone, scores it 1.4:1. So it is held to a perceptual
// distance instead. Refusing the second register on WCAG's number would be
// deferring to the wrong measurement, not to accessibility.
func TestLabelSetStandsOffItsCard(t *testing.T) {
	css := core(t)
	// The card an xy label is drawn on: --kanban-card, which xy's layer aliases
	// to --surface on light and --structure on dark.
	ground := map[string]string{"light": "surface", "dark": "structure"}
	for _, th := range themes[:2] { // the two plain themes; high contrast only lifts the floor
		t.Run(th.name, func(t *testing.T) {
			tokens := tokensFor(t, css, th.selector)
			card, _, ok := resolve(tokens, ground[th.name])
			if !ok {
				t.Fatalf("no %s in %s", ground[th.name], th.selector)
			}
			for _, c := range Set("label", th.name) {
				fill := fromHex(strings.TrimPrefix(c.Hex, "#"))
				if Tone("label", c.Name) == 0 {
					if got := Contrast(fill, card); got < 3.0 {
						t.Errorf("label %s is %s here and reaches only %.2f:1 on the card — pick a lighter rung",
							c.Name, c.Hex, got)
					}
					continue
				}
				if got := Distance(fill, card); got < 0.15 {
					t.Errorf("label %s is %s here and sits %.3f from the card on the OKLab solid — too close to see",
						c.Name, c.Hex, got)
				}
			}
		})
	}
}

// A deep tone that lands on its own default is a colour the picker shows twice.
// TestSetsAreOfferable catches the duplicate, but not what it means, so name it
// here — and assert the gap is wide enough to be a CHOICE, not a near-miss.
func TestLabelTonesAreDistinct(t *testing.T) {
	for _, theme := range Themes("label") {
		t.Run(theme, func(t *testing.T) {
			bright := map[string]string{}
			for _, c := range Set("label", theme) {
				if Tone("label", c.Name) == 0 {
					bright[c.Name] = c.Hex
					continue
				}
				base := strings.TrimSuffix(c.Name, "-deep")
				gap := Distance(fromHex(strings.TrimPrefix(c.Hex, "#")),
					fromHex(strings.TrimPrefix(bright[base], "#")))
				if gap < 0.1 {
					t.Errorf("%s (%s) sits %.3f from %s (%s) — too close to offer as a separate colour",
						c.Name, c.Hex, gap, base, bright[base])
				}
			}
		})
	}
}

// TestWarnInkReadsOnBothGrounds is the constraint the old four-token amber
// cluster had drifted toward without ever writing down. --warn-text appears in
// two places — inside the banner, on --warn-bg, and on bare paper for the
// fuzzy-match badge and the pending sync dot — so ONE ink has to clear AA on
// both. That is also why the fill is yellow and the ink is orange: uchu's
// darkest yellow reaches only 3.54:1 on yang, so yellow cannot be the ink.
func TestWarnInkReadsOnBothGrounds(t *testing.T) {
	css := core(t)
	for _, th := range themes {
		t.Run(th.name, func(t *testing.T) {
			tokens := tokensFor(t, css, th.selector)
			ink, _, ok1 := resolve(tokens, "warn-text")
			fill, _, ok2 := resolve(tokens, "warn-bg")
			surface, _, ok3 := resolve(tokens, "surface")
			if !ok1 || !ok2 || !ok3 {
				t.Fatal("the warning pair does not resolve")
			}
			for _, g := range []struct {
				name string
				bg   OKLCH
			}{{"--warn-bg", fill}, {"--surface", surface}} {
				if got := Contrast(ink, g.bg); got < 4.5 {
					t.Errorf("--warn-text on %s is %.2f:1, want >= 4.5:1", g.name, got)
				}
			}
			// The fill is a fill: it must not be mistaken for the paper.
			if math.Abs(fill.L-surface.L) < 0.02 {
				t.Errorf("--warn-bg (L %.3f) is indistinguishable from --surface (L %.3f)", fill.L, surface.L)
			}
		})
	}
}

// TestInkClearsItsSurface is the contrast floor. Each pair is one the Kit
// actually composes — body text on the two backgrounds text sits on, and the
// muted tier on both. WCAG AA is 4.5:1 for body text; --muted-light is
// decorative (disabled labels, placeholder chrome) and only has to stay visible.
func TestInkClearsItsSurface(t *testing.T) {
	pairs := []struct {
		ink, bg string
		min     float64
	}{
		{"text", "surface", 7},
		{"text", "structure", 7},
		{"text", "action-bg", 4.5},
		{"text-strong", "surface", 4.5},
		{"muted", "surface", 4.5},
		{"muted", "structure", 4.5},
		{"muted-light", "surface", 2.5},
	}
	css := core(t)
	for _, th := range themes {
		t.Run(th.name, func(t *testing.T) {
			tokens := tokensFor(t, css, th.selector)
			for _, p := range pairs {
				ink, d1, ok1 := resolve(tokens, p.ink)
				bg, d2, ok2 := resolve(tokens, p.bg)
				if !ok1 || !ok2 || d1 || d2 {
					t.Fatalf("--%s on --%s does not resolve", p.ink, p.bg)
				}
				if got := Contrast(ink, bg); got < p.min {
					t.Errorf("--%s on --%s is %.2f:1, want >= %.2f:1", p.ink, p.bg, got, p.min)
				}
			}
		})
	}
}

// TestHighContrastStretchesTheLadder is the property that lets the two contrast
// blocks be a stride instead of a palette. High contrast exists for a display
// that cannot separate adjacent fills, so the thing it must do is widen the
// steps — never narrow them — and pull the structural lines toward the ink.
//
// Comparing ink-to-fill contrast ratios does NOT capture this, which an earlier
// version of this test learned the hard way: high contrast darkens the ink too,
// so a ratio can fall while the design gets better. The gap between neighbours
// is the honest measure.
func TestHighContrastStretchesTheLadder(t *testing.T) {
	css := core(t)
	for _, tc := range []struct{ base, high string }{
		{"light", "light+contrast"},
		{"dark", "dark+contrast"},
	} {
		t.Run(tc.high, func(t *testing.T) {
			var regular, high []string
			for _, th := range themes {
				switch th.name {
				case tc.base:
					regular = th.selector
				case tc.high:
					high = th.selector
				}
			}
			was, now := ladder(t, css, regular), ladder(t, css, high)
			if minGap(now) < minGap(was) {
				t.Errorf("high contrast COMPRESSES the ladder: tightest step %.3f → %.3f",
					minGap(was), minGap(now))
			}

			// A ladder that is already wide has nothing to prove — dark walks
			// yin in ~0.09 steps and stays put. A TIGHT one must open up: light
			// steps gray by 0.035, which is the case this mode exists for.
			const tight = 0.06
			if minGap(was) < tight && minGap(now) <= minGap(was) {
				t.Errorf("the regular ladder is tight (%.3f) so high contrast must widen it, got %.3f",
					minGap(was), minGap(now))
			}

			// Lines carry the separation the fills cannot, so they must move
			// toward the ink.
			rt, ht := tokensFor(t, css, regular), tokensFor(t, css, high)
			surface, _, _ := resolve(ht, "surface")
			for _, line := range []string{"grid", "grid-dark"} {
				a, _, _ := resolve(rt, line)
				b, _, _ := resolve(ht, line)
				if Contrast(b, surface) <= Contrast(a, surface) {
					t.Errorf("--%s must read harder against --surface in high contrast: %.2f:1 → %.2f:1",
						line, Contrast(a, surface), Contrast(b, surface))
				}
			}
		})
	}
}
