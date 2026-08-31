package pptx

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
)

// TestParity compares every slide's XML against the presentation chgksuite
// builds from the same package (scripts/gen_pptx_oracles.py).
func TestParity(t *testing.T) {
	sources, _ := filepath.Glob("testdata/*.4s")
	for _, src := range sources {
		name := strings.TrimSuffix(filepath.Base(src), ".4s")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			want := readOracle(t, "testdata/"+name+"__pptx")
			images := map[string][]byte{}
			pics, _ := filepath.Glob("testdata/*.png")
			for _, p := range pics {
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				images[filepath.Base(p)] = data
			}
			opts := Options{
				Language: "ru",
				FontDirs: []string{"/usr/share/fonts/truetype/msttcorefonts"},
			}
			// service.4s carries a real config and a real template: a deck whose
			// every tour opens on a slide the template drew by hand. It is the
			// only fixture that exercises the service slides, the numbered tour
			// stubs, a multi-slide template and the cloning that goes with them.
			if name == "service" {
				cfgRaw, err := os.ReadFile("testdata/service_config.toml")
				if err != nil {
					t.Fatal(err)
				}
				if opts.Config, err = ParseConfig(string(cfgRaw)); err != nil {
					t.Fatal(err)
				}
				if opts.Template, err = os.ReadFile("testdata/service_template.pptx"); err != nil {
					t.Fatal(err)
				}
			}
			out, err := Export(fsource.Parse(string(raw), "chgk"), images, opts)
			if err != nil {
				t.Fatal(err)
			}
			got := partsOf(t, out)
			for _, part := range sortedKeys(want) {
				a, b := want[part], got[part]
				if a == b {
					continue
				}
				// An inline picture is placed from measured text, so its offset
				// carries the measurement's residual — see measure.go. Everything
				// else must match to the byte; this must match to the pixel.
				if drift, only := pictureOffsetDrift(a, b); only {
					if drift > emuPerInch/pxPerInch {
						t.Errorf("%s: an inline picture moved %d EMU, over a pixel", part, drift)
					}
					continue
				}
				t.Errorf("%s differs:\nwant %s\n got %s", part,
					firstDiff(a, b), firstDiff(b, a))
				return
			}
			if len(got) != len(want) {
				t.Errorf("%d parts, want %d", len(got), len(want))
			}
		})
	}
}

var reInteresting = regexp.MustCompile(`^(ppt/slides/|ppt/presentation\.xml|\[Content_Types\])`)

func partsOf(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		if !reInteresting.MatchString(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func readOracle(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Skipf("no oracle in %s: run scripts/gen_pptx_oracles.py", dir)
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// firstDiff shows where two documents part company, with a little either side.
func firstDiff(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	start := max(0, i-60)
	end := min(len(a), i+140)
	return "…" + a[start:end] + "…"
}

var (
	rePicOffset = regexp.MustCompile(`<a:off x="(-?\d+)" y="(-?\d+)"/>`)
	rePicture   = regexp.MustCompile(`(?s)<p:pic>.*?</p:pic>`)
)

// pictureOffsetDrift reports the largest a picture moved between the two, and
// whether that is the whole of the difference between them.
func pictureOffsetDrift(want, got string) (drift int, only bool) {
	strip := func(s string) (string, []int) {
		var offsets []int
		out := rePicture.ReplaceAllStringFunc(s, func(pic string) string {
			return rePicOffset.ReplaceAllStringFunc(pic, func(off string) string {
				m := rePicOffset.FindStringSubmatch(off)
				for _, v := range m[1:] {
					n, _ := strconv.Atoi(v)
					offsets = append(offsets, n)
				}
				return `<a:off/>`
			})
		})
		return out, offsets
	}
	a, wantOffsets := strip(want)
	b, gotOffsets := strip(got)
	if a != b || len(wantOffsets) != len(gotOffsets) {
		return 0, false
	}
	for i := range wantOffsets {
		if d := abs(wantOffsets[i] - gotOffsets[i]); d > drift {
			drift = d
		}
	}
	return drift, true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
