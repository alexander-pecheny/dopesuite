package openquiz

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/imghost"
)

// stubHost is the link chgksuite's imgur was stubbed with when the oracles were
// recorded (scripts/gen_export_oracles.py): what is under test is what the
// exporter does with a link, not the upload.
type stubHost struct{}

func (stubHost) Upload(name string, _ []byte) (string, error) {
	return "https://img.example/" + filepath.Base(name), nil
}

// TestParity compares the exported JSON against chgksuite's own, byte for byte.
func TestParity(t *testing.T) {
	sources, _ := filepath.Glob("testdata/*.4s")
	for _, src := range sources {
		name := strings.TrimSuffix(filepath.Base(src), ".4s")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile("testdata/" + name + "__openquiz.canon")
			if err != nil {
				t.Skipf("no oracle: %v", err)
			}
			images, err := loadImages(t)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Export(fsource.Parse(string(raw), "chgk"), images, stubHost{}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("mismatch:\nwant %s\n got %s", string(want), string(got))
			}
		})
	}
}

func loadImages(t *testing.T) (map[string][]byte, error) {
	t.Helper()
	images := map[string][]byte{}
	names, _ := filepath.Glob("testdata/*.png")
	for _, n := range names {
		data, err := os.ReadFile(n)
		if err != nil {
			return nil, err
		}
		images[filepath.Base(n)] = data
	}
	return images, nil
}

var _ imghost.Host = stubHost{}
