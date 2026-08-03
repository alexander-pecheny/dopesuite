package buildinfo

import "testing"

func TestDescribe(t *testing.T) {
	cases := []struct {
		name     string
		stamped  string
		revision string
		modified bool
		want     string
	}{
		{"stamped tag wins", "xy/2026.08.03", "abcdef1234567890", false, "xy/2026.08.03"},
		{"stamped tag wins over dirty vcs", "xy/2026.08.03", "abcdef1234567890", true, "xy/2026.08.03"},
		{"unstamped falls back to revision", "", "abcdef1234567890", false, "dev-abcdef1"},
		{"dirty revision is marked", "", "abcdef1234567890", true, "dev-abcdef1-dirty"},
		{"short revision is not padded", "", "abc", false, "dev-abc"},
		{"nothing at all", "", "", false, "dev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := describe(c.stamped, c.revision, c.modified); got != c.want {
				t.Errorf("describe(%q, %q, %v) = %q, want %q", c.stamped, c.revision, c.modified, got, c.want)
			}
		})
	}
}

func TestVersionIsNeverEmpty(t *testing.T) {
	if Version() == "" {
		t.Error("Version() is empty")
	}
}
