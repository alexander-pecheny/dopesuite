package main

import (
	"reflect"
	"sort"
	"testing"
)

func emittedFrom(t *testing.T, src string) []string {
	t.Helper()
	got := sites{}
	emitSites(lexTS(src), "x.ts", got)
	out := make([]string, 0, len(got))
	for name := range got {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestEmitSites(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{{
		name: "assignment",
		src:  `el.className = "results-table od-results-table";`,
		want: []string{"od-results-table", "results-table"},
	}, {
		// The bug a regex without a left boundary shipped: activeClass ends in
		// "Class", so `class(Name)?\s*=` matched and "results" read as a class.
		name: "identifier merely ending in Class",
		src:  `const activeClass = "results"; let cssClass = "detailed";`,
		want: []string{},
	}, {
		// toggle's second argument is a force flag; its strings are conditions.
		name: "toggle force argument",
		src:  `f.classList.toggle("results-scroll-left", tab === "results" && f.scrollLeft > 1);`,
		want: []string{"results-scroll-left"},
	}, {
		name: "add is variadic over classes",
		src:  `w.classList.add("menu-inline", "menu-public");`,
		want: []string{"menu-inline", "menu-public"},
	}, {
		name: "template keeps static chunks and drops interpolations",
		src:  "box.className = `grid-match ${liveMatch?.status || \"pending\"}`;",
		want: []string{"grid-match"},
	}, {
		name: "nested call is not an argument separator",
		src:  `e.classList.add(pick(a, b) ? "seed-team-cell" : "seed-team-city");`,
		want: []string{"seed-team-cell", "seed-team-city"},
	}, {
		name: "setAttribute class",
		src:  `n.setAttribute("class", "roster-player");`,
		want: []string{"roster-player"},
	}, {
		name: "setAttribute of anything else",
		src:  `n.setAttribute("data-state", "roster-player");`,
		want: []string{},
	}, {
		name: "markup built as a string",
		src:  "root.innerHTML = `<div class=\"pv-list\"><span class=\"pv-num\"></span></div>`;",
		want: []string{"pv-list", "pv-num"},
	}, {
		name: "comments are not code",
		src:  `// el.className = "commented-out";` + "\n/* el.className = \"blocked\"; */",
		want: []string{},
	}, {
		name: "regex literal does not swallow the next quote",
		src:  `const re = /["']/g; el.className = "after-regex";`,
		want: []string{"after-regex"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := emittedFrom(t, tc.src)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("emitted %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScanGo(t *testing.T) {
	src := `package ui

func expand(p *Element) []Node {
	mainCls := []string{"game-header-main"}
	if Flag(p, "viewer") {
		mainCls = append(mainCls, "viewer-header-main")
	}
	classes := []string{"btn"}
	classes = append(classes, "btn-ghost")
	_ = El("div", ClassAttr("host-top", "host-actions"))
	_ = spec{Tag: "h2", Classes: []string{"card-detail-title"}}
	_ = FlexClasses("u-col", p)
	_ = strings.CutPrefix(token, "seed-")
	return nil
}`
	got := sites{}
	scanGo(src, "expand.go", got)
	out := make([]string, 0, len(got))
	for name := range got {
		out = append(out, name)
	}
	sort.Strings(out)
	want := []string{
		"btn", "btn-ghost", "card-detail-title", "game-header-main",
		"host-actions", "host-top", "u-col", "viewer-header-main",
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("scanGo found %q, want %q", out, want)
	}
}

func TestStyledClasses(t *testing.T) {
	css := `
/* .commented-out { color: red } */
.results-table { border-collapse: separate; }
.a, .b > .c:hover { color: var(--text); }
.logo { background: url(brand.png); font-family: "Noto Sans"; }
@media (max-width: 34rem) { .mobile-only { display: none; } }
`
	got := styledClasses(css)
	sort.Strings(got)
	want := []string{"a", "b", "c", "logo", "mobile-only", "results-table"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("styled %q, want %q", got, want)
	}
}

func TestComposedFromPrefix(t *testing.T) {
	// helpers.go builds these as "u-align-" + value from a closed enum, so the
	// whole name never appears as a literal anywhere.
	lits := sites{}
	lits.add("u-align-", "helpers.go")
	if !composedFromPrefix("u-align-center", lits) {
		t.Error("prefix literal should cover the names built from it")
	}
	if composedFromPrefix("btn-primary", lits) {
		t.Error("unrelated prefix must not cover it")
	}
	// A prefix must end at a "-" boundary: "u-al" is not how these are built.
	partial := sites{}
	partial.add("u-al", "x.go")
	if composedFromPrefix("u-align-center", partial) {
		t.Error("a mid-segment prefix must not count")
	}
}
