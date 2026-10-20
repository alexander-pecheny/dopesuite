package htmlshot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWidthMM(t *testing.T) {
	if got, err := WidthMM("body { width: 66.7mm; }"); err != nil || got != 66.7 {
		t.Errorf("= %v, %v", got, err)
	}
	if _, err := WidthMM("body { width: 400px; }"); err == nil {
		t.Error("a width in pixels is not a page size")
	}
}

func TestWithPageRule(t *testing.T) {
	got := withPageRule("<html><head><title>x</title></head><body></body></html>", 66.7, 54.0)
	if !strings.Contains(got, "@page { size: 66.70mm 54.00mm; margin: 0; }") {
		t.Errorf("no page rule: %s", got)
	}
	if strings.Index(got, "@page") > strings.Index(got, "</head>") {
		t.Error("the rule must land inside the head")
	}
	if !strings.Contains(got, "print-color-adjust: exact") {
		t.Error("the backgrounds would not print")
	}
}

func TestMillimetresAndPixelsRoundTrip(t *testing.T) {
	// Playwright's own arithmetic: 96 CSS pixels to the inch.
	if got := mmToPx(66.7); got != 252 {
		t.Errorf("mmToPx(66.7) = %d, want 252", got)
	}
	if got := pxToMM(252); got < 66.6 || got > 66.8 {
		t.Errorf("pxToMM(252) = %v", got)
	}
}

// TestRender drives a real browser, so it is skipped where there is none.
func TestRender(t *testing.T) {
	browser, err := FindBrowser("")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "handout.html")
	// The scaffold create_html writes, whose box-sizing keeps the padding
	// inside the declared width — without it the content is wider than the
	// body, and the PDF is printed at the content's width, as Playwright's is.
	html := `<!DOCTYPE html><html><head><meta charset="utf-8"><style>
* { margin: 0; padding: 0; box-sizing: border-box; }
html, body { width: 66.7mm; } body { font-size: 14pt; padding: 2mm; }
</style></head><body><p>Раздатка, достаточно длинная, чтобы занять пару строк.</p></body></html>`
	if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Render(context.Background(), path, Options{Browser: browser, Scale: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{res.PDF, res.PNG} {
		info, err := os.Stat(out)
		if err != nil {
			t.Fatalf("%s: %v", out, err)
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", out)
		}
	}
	if res.WidthMM < 66 || res.WidthMM > 68 {
		t.Errorf("width = %v mm, want the body's 66.7", res.WidthMM)
	}
	if res.HeightMM <= 0 {
		t.Errorf("height = %v mm", res.HeightMM)
	}
}
