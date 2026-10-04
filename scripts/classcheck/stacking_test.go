package main

import "testing"

func TestStackingProblems(t *testing.T) {
	got := stackingProblems(`
.name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.clip { overflow: hidden; text-overflow: clip; }
.results-team:hover,
.results-team:focus-within { z-index: var(--z-popover); }
.player-cell:hover { background: var(--paper); }
.sticky { position: sticky; z-index: 5; }
/* .x:hover { z-index: 3 } stays a comment */
`)
	want := []string{".name", ".results-team:hover, .results-team:focus-within"}
	if len(got) != len(want) {
		t.Fatalf("problems = %+v, want %d", got, len(want))
	}
	for i, p := range got {
		if p.selector != want[i] {
			t.Errorf("problem %d on %q, want %q", i, p.selector, want[i])
		}
	}
}
