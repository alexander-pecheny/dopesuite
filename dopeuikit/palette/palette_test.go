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
)

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
		if derivedRe.MatchString(v) {
			return OKLCH{}, true, true
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
				// A rung reference always round-trips to its ramp value; a hex
				// only lands on one by luck.
				if !onARamp(c) {
					t.Errorf("--%s resolves to L=%.4f C=%.4f, which is not a uchu rung — name a --uchu-* rung instead of a literal",
						role, c.L, c.C)
				}
			}
		})
	}
}

// onARamp reports whether a colour is exactly a vendored rung.
func onARamp(c OKLCH) bool {
	const eps = 1e-9
	for _, name := range Anchors() {
		if a := Anchor(name); math.Abs(a.L-c.L) < eps && math.Abs(a.C-c.C) < eps {
			return true
		}
	}
	for _, variant := range Variants() {
		for _, hue := range Hues(variant) {
			for n := 1; n <= 9; n++ {
				r := Rung(variant, hue, n)
				if math.Abs(r.L-c.L) < eps && math.Abs(r.C-c.C) < eps && math.Abs(r.H-c.H) < eps {
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
