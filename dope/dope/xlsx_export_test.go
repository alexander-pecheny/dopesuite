package dopeserver

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// TestFestRouterServesXLSX drives the full public route — /fest/{ref}/game/{ref}.xlsx
// — through handleFestRouter, asserting it returns a real workbook with the
// attachment headers rather than falling through to the SPA viewer HTML.
func TestFestRouterServesXLSX(t *testing.T) {
	srv := newAuthTestServer(t)
	_, gameID := scopedAPITestIDs(t, srv)
	if _, err := srv.db.Exec(`update games set slug = 'ek' where id = ?`, gameID); err != nil {
		t.Fatalf("set game slug: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fest/fixture-ek/game/ek.xlsx", nil)
	resp := httptest.NewRecorder()
	srv.handleFestRouter(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("xlsx route status = %d, body %s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := resp.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	f, err := excelize.OpenReader(bytes.NewReader(resp.Body.Bytes()))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()
	if sheets := f.GetSheetList(); len(sheets) == 0 {
		t.Fatalf("workbook has no sheets")
	}
}

func TestExportFileNameAndDisposition(t *testing.T) {
	if got := exportFileStem("мой-фест", 1, "финал-2026", 2); got != "мой-фест-финал-2026" {
		t.Fatalf("exportFileStem with slugs = %q", got)
	}
	if got := exportFileStem("", 3, "", 7); got != "fest3-game7" {
		t.Fatalf("exportFileStem fallback = %q", got)
	}
	if got := exportFileStem("фест", 3, "", 7); got != "фест-game7" {
		t.Fatalf("exportFileStem mixed = %q", got)
	}
	cd := contentDispositionAttachment("Финал.xlsx")
	if !strings.Contains(cd, "filename*=UTF-8''") || !strings.Contains(cd, "attachment;") {
		t.Fatalf("disposition = %q", cd)
	}
}
