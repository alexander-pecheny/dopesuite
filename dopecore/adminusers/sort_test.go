package adminusers

import (
	"net/url"
	"testing"
)

func TestParseSort(t *testing.T) {
	cases := []struct {
		query string
		want  Sort
	}{
		{"", Sort{}},
		{"sort=nonsense", Sort{}},
		{"dir=asc", Sort{}},
		{"sort=used", Sort{Key: "used", Desc: true}},
		{"sort=used&dir=asc", Sort{Key: "used"}},
		{"sort=last&dir=desc", Sort{Key: "last", Desc: true}},
	}
	for _, c := range cases {
		q, err := url.ParseQuery(c.query)
		if err != nil {
			t.Fatalf("ParseQuery(%q): %v", c.query, err)
		}
		if got := ParseSort(q, "used", "last"); got != c.want {
			t.Errorf("ParseSort(%q) = %+v, want %+v", c.query, got, c.want)
		}
	}
}

// TestSortHeader checks the flip: the active column offers the other direction,
// the others always offer descending first.
func TestSortHeader(t *testing.T) {
	dir, arrow := Sort{Key: "used", Desc: true}.Header("used")
	if dir != "asc" || arrow != " ↓" {
		t.Errorf("active desc = (%q, %q), want (asc, ↓)", dir, arrow)
	}
	dir, arrow = Sort{Key: "used"}.Header("used")
	if dir != "desc" || arrow != " ↑" {
		t.Errorf("active asc = (%q, %q), want (desc, ↑)", dir, arrow)
	}
	dir, arrow = Sort{Key: "used", Desc: true}.Header("last")
	if dir != "desc" || arrow != "" {
		t.Errorf("inactive = (%q, %q), want (desc, \"\")", dir, arrow)
	}
}
