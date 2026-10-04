package main

import "testing"

func TestHeadRowProblems(t *testing.T) {
	got := headRowProblems(`
.entry-table thead tr:nth-child(2) th { top: 28px; height: 22px; }
.multi-table thead tr:nth-child(2) th { vertical-align: bottom; }
.match-table thead th { position: sticky; top: 0; }
.x thead tr:first-child th { padding-top: 2px; }
/* .y thead tr:nth-child(3) th { top: 56px } stays a comment */
`)
	if len(got) != 1 || got[0].selector != ".entry-table thead tr:nth-child(2) th" {
		t.Fatalf("problems = %+v, want the entry table's second row only", got)
	}
}

func TestTheadOutsideSheetHead(t *testing.T) {
	src := `const thead = document.createElement("thead");`
	if n := theadOutsideSheetHead("dope/dope/web/ts/od.ts", src); n != 1 {
		t.Errorf("od.ts: %d, want 1", n)
	}
	if n := theadOutsideSheetHead(sheetHeadOwner, src); n != 0 {
		t.Errorf("sheet-pins.ts: %d, want 0", n)
	}
	if n := theadOutsideSheetHead("xy/web/ts/board.ts", src); n != 0 {
		t.Errorf("xy: %d, want 0", n)
	}
	if n := theadOutsideSheetHead("dope/dope/web/ts/x.ts", `table.createTHead()`); n != 1 {
		t.Errorf("createTHead: %d, want 1", n)
	}
}
