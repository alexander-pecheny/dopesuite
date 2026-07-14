package gameexport

import (
	"strings"
	"testing"
)

func TestExportFileNameAndDisposition(t *testing.T) {
	if got := ExportFileStem("мой-фест", 1, "финал-2026", 2); got != "мой-фест-финал-2026" {
		t.Fatalf("ExportFileStem with slugs = %q", got)
	}
	if got := ExportFileStem("", 3, "", 7); got != "fest3-game7" {
		t.Fatalf("ExportFileStem fallback = %q", got)
	}
	if got := ExportFileStem("фест", 3, "", 7); got != "фест-game7" {
		t.Fatalf("ExportFileStem mixed = %q", got)
	}
	cd := ContentDispositionAttachment("Финал.xlsx")
	if !strings.Contains(cd, "filename*=UTF-8''") || !strings.Contains(cd, "attachment;") {
		t.Fatalf("disposition = %q", cd)
	}
}
