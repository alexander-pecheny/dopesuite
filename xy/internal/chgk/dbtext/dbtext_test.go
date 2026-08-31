package dbtext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/imghost"
)

// stubHost is the link chgksuite's imgur was stubbed with when the oracles were
// recorded (scripts/gen_export_oracles.py).
type stubHost struct{}

func (stubHost) Upload(name string, _ []byte) (string, error) {
	return "https://img.example/" + filepath.Base(name), nil
}

var _ imghost.Host = stubHost{}

// TestParity compares the base's plain text against chgksuite's own.
func TestParity(t *testing.T) {
	// The oracles were recorded on a day later than every date in them, so the
	// "a date in the future is last year's" rule stays out of it.
	old := today
	today = func() time.Time { return time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { today = old })

	sources, _ := filepath.Glob("testdata/*.4s")
	for _, src := range sources {
		name := strings.TrimSuffix(filepath.Base(src), ".4s")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile("testdata/" + name + "__base.canon")
			if err != nil {
				t.Skipf("no oracle: %v", err)
			}
			images := map[string][]byte{}
			pics, _ := filepath.Glob("testdata/*.png")
			for _, p := range pics {
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				images[filepath.Base(p)] = data
			}
			got, err := Export(fsource.Parse(string(raw), "chgk"), images, stubHost{}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("mismatch:\nwant %q\n got %q", string(want), got)
			}
		})
	}
}
