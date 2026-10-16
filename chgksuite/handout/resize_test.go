package handout

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// chgksuiteFonts is where the reference tool keeps its Noto Sans. The fit is
// decided at the millimetre, so the two tools only agree when they typeset with
// the same faces — xy's own bundle carries a transplanted pause glyph and lays a
// row out a hair differently.
func chgksuiteFonts() string {
	if d := os.Getenv("XY_CHGKSUITE_FONTS"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "chgksuite", "chgksuite", "chgksuite", "resources", "fonts")
}

// TestResizeParity checks the image-shrink pass against chgksuite's own run on
// the same handout:
//
//	chgksuite handouts split_fit testdata/resize.hndt --output_dir …
//	→ "image resize: 1 -> 0.97, rows 4 -> 5"
//
// Needs the typst binary (XY_TYPST_TEST_BIN) and chgksuite's fonts.
func TestResizeParity(t *testing.T) {
	bin := os.Getenv("XY_TYPST_TEST_BIN")
	if bin == "" {
		t.Skip("set XY_TYPST_TEST_BIN to run the real fit")
	}
	fonts := chgksuiteFonts()
	if _, err := os.Stat(fonts); err != nil {
		t.Skipf("no chgksuite fonts at %s", fonts)
	}
	src, err := os.ReadFile("testdata/resize.hndt")
	if err != nil {
		t.Fatal(err)
	}
	img, err := os.ReadFile("testdata/resize.png")
	if err != nil {
		t.Fatal(err)
	}

	ts, err := newCLITypesetter(bin, fonts)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()

	ctx := context.Background()
	r, err := newSFRun(ctx, map[string][]byte{"resize.png": img}, DefaultArgs(), ts)
	if err != nil {
		t.Fatal(err)
	}
	blocks := parseSFBlocks(string(src))
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(blocks))
	}
	rows, upd, err := r.fitBlock(ctx, blocks[0])
	if err != nil {
		t.Fatal(err)
	}
	if rows != 5 {
		t.Errorf("rows = %d, chgksuite fits 5", rows)
	}
	if upd["resize_image"] == nil || *upd["resize_image"] != "0.97" {
		t.Errorf("resize_image = %v, chgksuite shrinks to 0.97", deref(upd["resize_image"]))
	}
}

func deref(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}
