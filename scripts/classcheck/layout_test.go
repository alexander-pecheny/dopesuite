package main

import (
	"reflect"
	"testing"
)

func TestLayoutOnlyClasses(t *testing.T) {
	got := layoutOnlyClasses(`
.filter-modes { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
.invite-row { display: flex; flex-direction: column; gap: var(--space-1); padding: var(--space-2) 0; }
.klist-count { font-size: var(--text-xs); color: var(--muted); }
.color-grid { display: grid; grid-template-columns: repeat(13, 20px); gap: 3px; }
.odd-gap { display: flex; gap: 7px; }
.member-row .member-name { display: flex; gap: var(--space-1); }
.tl-versions { display: flex; flex-direction: column; gap: var(--space-1); }
`)
	var names []string
	for _, r := range got {
		names = append(names, r.class)
	}
	// .invite-row carries padding, .klist-count is typography, .color-grid is a
	// grid the utilities have no name for, .odd-gap's gap is off the scale, and a
	// descendant selector is not a bare class.
	if want := []string{"filter-modes", "tl-versions"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("layout-only = %v, want %v", names, want)
	}
}

func TestLayoutUtilitiesNamesTheComposition(t *testing.T) {
	utils, ok := layoutUtilities("display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap")
	if !ok {
		t.Fatal("a pure layout body should be recognised")
	}
	if want := []string{"u-align-center", "u-gap-sm", "u-row", "u-wrap"}; !reflect.DeepEqual(utils, want) {
		t.Fatalf("utilities = %v, want %v", utils, want)
	}
	// A column says which way it runs, so the row utility is dropped from the advice.
	if got := dedupe([]string{"u-col", "u-gap-xs", "u-row"}); !reflect.DeepEqual(got, []string{"u-col", "u-gap-xs"}) {
		t.Fatalf("dedupe = %v, want the column without the row", got)
	}
	if _, ok := layoutUtilities("display: block; gap: var(--space-2)"); ok {
		t.Error("display:block is not a flex utility")
	}
	if _, ok := layoutUtilities("gap: var(--space-2)"); ok {
		t.Error("a bare gap (a grid, say) is not this check's business")
	}
}
