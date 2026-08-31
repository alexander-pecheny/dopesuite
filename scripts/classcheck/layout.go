package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The layout ratchet. The kit ships .u-col/.u-row/.u-gap-*/.u-align-*/.u-justify-*
// for exactly this, and the most common way a feature drifts out of the design
// system is minting a class whose whole body is display:flex + gap + alignment.
// The referential check above cannot see it: such a class is both styled and
// emitted, so it passes clean.
//
// A blanket rule would be wrong — for a named component (.label-picker is «the
// row of label chips») the layout IS the identity. So the existing ones are
// grandfathered in layout-baseline.txt and this only refuses the NEXT one, with
// the composition to use instead. Adding a line to the baseline is allowed; it
// just has to be a decision somebody made rather than one nobody noticed.

var reBareClassSel = regexp.MustCompile(`^\.([a-z][a-z0-9-]*)$`)

// utilityFor maps a layout declaration onto the utility that already draws it.
// Only these properties count as "pure layout"; anything else in the body means
// the class is carrying something of its own.
var utilityFor = map[string]func(value string) string{
	"display":        func(v string) string { return map[string]string{"flex": "u-row"}[v] },
	"flex-direction": func(v string) string { return map[string]string{"column": "u-col", "row": "u-row"}[v] },
	"flex-wrap":      func(v string) string { return map[string]string{"wrap": "u-wrap"}[v] },
	"gap":            gapUtility,
	"align-items": func(v string) string {
		return map[string]string{"flex-start": "u-align-start", "center": "u-align-center", "flex-end": "u-align-end"}[v]
	},
	"justify-content": func(v string) string {
		return map[string]string{"center": "u-justify-center", "flex-end": "u-justify-end", "space-between": "u-justify-between"}[v]
	},
}

func gapUtility(v string) string {
	return map[string]string{
		"var(--space-1)": "u-gap-xs", "var(--space-2)": "u-gap-sm", "var(--space-3)": "u-gap-md",
		"var(--space-4)": "u-gap-lg", "var(--space-6)": "u-gap-xl",
	}[v]
}

type layoutRule struct {
	class string
	utils []string
}

// layoutOnlyClasses finds every rule whose selector is one bare class and whose
// body says nothing but layout. The sheet is walked the same way styledClasses
// walks it, so a declaration value can never be read as a selector.
func layoutOnlyClasses(src string) []layoutRule {
	var out []layoutRule
	for _, m := range regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(stripComments(src), -1) {
		sel := strings.TrimSpace(m[1])
		name := reBareClassSel.FindStringSubmatch(sel)
		if name == nil {
			continue
		}
		utils, ok := layoutUtilities(m[2])
		if !ok {
			continue
		}
		out = append(out, layoutRule{class: name[1], utils: utils})
	}
	return out
}

// layoutUtilities returns the utility composition a body is equivalent to, and
// false when the body carries anything that is not layout (or a layout value the
// utilities have no name for — an off-scale gap is its own decision).
func layoutUtilities(body string) ([]string, bool) {
	var utils []string
	flex := false
	for _, decl := range strings.Split(body, ";") {
		prop, value, ok := strings.Cut(decl, ":")
		if !ok {
			if strings.TrimSpace(decl) != "" {
				return nil, false
			}
			continue
		}
		prop, value = strings.TrimSpace(prop), strings.TrimSpace(value)
		fn, known := utilityFor[prop]
		if !known {
			return nil, false
		}
		if prop == "display" {
			if value != "flex" {
				return nil, false // display:block and friends are not a flex utility
			}
			flex = true
		}
		u := fn(value)
		if u == "" {
			return nil, false
		}
		utils = append(utils, u)
	}
	if !flex || len(utils) == 0 {
		return nil, false // a bare `gap` on a grid, say, is not this
	}
	sort.Strings(utils)
	return utils, true
}

// reportLayoutClasses prints the layout-only classes a sheet has grown since the
// baseline, and returns how many it found.
func reportLayoutClasses(sheet, src string, baseline map[string]bool) int {
	n := 0
	for _, r := range layoutOnlyClasses(src) {
		if baseline[r.class] {
			continue
		}
		n++
		fmt.Printf("%s: re-invented layout — .%s is only %s; compose the kit's utilities instead\n",
			sheet, r.class, strings.Join(dedupe(r.utils), " "))
	}
	return n
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "u-row" && seen["u-col"] {
			continue // flex-direction:column already said which way it runs
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
