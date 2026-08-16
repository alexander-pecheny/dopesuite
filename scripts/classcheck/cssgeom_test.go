package main

import "testing"

func TestGridLiterals(t *testing.T) {
	src := `
:root { --grid-row: 24px; }
.grid-slot-cell { height: var(--grid-row); line-height: 16px; }
.grid-standings th, .grid-standings td { padding: var(--grid-cell-pad); }
.match-table td { width: 90px; }
@media (max-width: 760px) {
  :root { --fest-col-min: 170px; }
  .fest-columns { grid-auto-columns: minmax(170px, 1fr); }
  .grid-matches { gap: 0; }
  .grid-slot-team-name { overflow-x: auto; }
}
`
	hits := gridLiterals(src)
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d: %+v", len(hits), hits)
	}
	if hits[0].decl != "line-height: 16px" || hits[0].selector != ".grid-slot-cell" {
		t.Errorf("first hit = %+v", hits[0])
	}
	if hits[1].decl != "grid-auto-columns: minmax(170px, 1fr)" || hits[1].line != 8 {
		t.Errorf("second hit = %+v", hits[1])
	}
}
