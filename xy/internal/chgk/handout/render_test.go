package handout

import (
	"bytes"
	"context"
	"os"
	"testing"
)

// TestRenderReal compiles a real PDF via the typst binary when one is available
// (XY_TYPST_TEST_BIN), exercising render.go end-to-end (embedded fonts + the
// generated .typ). Skipped when typst isn't reachable.
func TestRenderReal(t *testing.T) {
	bin := os.Getenv("XY_TYPST_TEST_BIN")
	if bin == "" {
		t.Skip("set XY_TYPST_TEST_BIN to run the real typst render")
	}
	ts, err := NewCLITypesetter(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()
	hndt := "for_question: 12\ncolumns: 3\n\nРо-шам-бо\n"
	pdf, err := Render(context.Background(), hndt, nil, DefaultArgs(), ts)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("not a PDF (len=%d, prefix=%q)", len(pdf), pdf[:min(8, len(pdf))])
	}
	t.Logf("rendered %d-byte PDF", len(pdf))
}
