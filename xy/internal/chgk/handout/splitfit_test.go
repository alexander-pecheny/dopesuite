package handout

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// TestSplitFitReal runs the real typst fit and checks the invariant: every
// per-question PDF is exactly one page (the densest layout that still fits), and
// every output is a valid PDF. Set XY_TYPST_TEST_BIN to run.
func TestSplitFitReal(t *testing.T) {
	bin := os.Getenv("XY_TYPST_TEST_BIN")
	if bin == "" {
		t.Skip("set XY_TYPST_TEST_BIN to run the real split_fit")
	}
	src := "for_question: 1\ncolumns: 3\n\nКороткий\n" +
		"---\nfor_question: 7\ncolumns: 3\n\nПобольше текста для второго вопроса, чтобы он занял заметно больше места и поместилось меньше строк на странице.\n" +
		"---\nfor_question: 13\ncolumns: 6\nhandouts_per_team: 2\n\nКоманде\n"

	ts, err := NewCLITypesetter(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()
	zipBytes, err := SplitFit(context.Background(), src, nil, DefaultArgs(), ts)
	if err != nil {
		t.Fatalf("SplitFit: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		if !bytes.HasPrefix(data, []byte("%PDF")) {
			t.Errorf("%s is not a PDF", f.Name)
			continue
		}
		pages, err := api.PageCount(bytes.NewReader(data), model.NewDefaultConfiguration())
		if err != nil {
			t.Errorf("%s page count: %v", f.Name, err)
			continue
		}
		if strings.HasPrefix(f.Name, "q") && pages != 1 {
			t.Errorf("%s should be 1 page, got %d", f.Name, pages)
		}
		t.Logf("%s: %d page(s), %d bytes", f.Name, pages, len(data))
	}
	for _, want := range []string{"q01.pdf", "q07.pdf", "q13.pdf", "all_q.pdf"} {
		if !names[want] {
			t.Errorf("missing %s in zip", want)
		}
	}
}

// TestFitRowsReal logs the chosen row counts (compare against chgksuite's
// "final rows=N" by eye / the manual parity check).
func TestFitRowsReal(t *testing.T) {
	bin := os.Getenv("XY_TYPST_TEST_BIN")
	if bin == "" {
		t.Skip("set XY_TYPST_TEST_BIN")
	}
	src := os.Getenv("XY_SPLITFIT_SRC")
	if src == "" {
		src = "for_question: 1\ncolumns: 3\n\nКороткий\n---\nfor_question: 2\ncolumns: 3\n\nДлинный текст раздаточного материала для второго вопроса.\n"
	}
	ts, err := NewCLITypesetter(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()
	rows, err := FitRows(context.Background(), src, nil, DefaultArgs(), ts)
	if err != nil {
		t.Fatalf("FitRows: %v", err)
	}
	t.Logf("fitted rows: %v", rows)
}
