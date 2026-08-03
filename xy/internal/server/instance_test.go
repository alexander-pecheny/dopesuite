package server

import "testing"

func TestCheckPublicURL(t *testing.T) {
	cases := []struct {
		name    string
		prod    bool
		raw     string
		wantErr bool
	}{
		{"production without it", true, "", true},
		{"production with it", true, "https://xy.example.org", false},
		{"development without it", false, "", false},
		{"production with blanks only", true, "   ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkPublicURL(c.prod, c.raw)
			if (err != nil) != c.wantErr {
				t.Errorf("checkPublicURL(%v, %q) = %v, wantErr %v", c.prod, c.raw, err, c.wantErr)
			}
		})
	}
}

func TestTrimPublicURL(t *testing.T) {
	cases := map[string]string{
		"https://xy.example.org/":   "https://xy.example.org",
		"https://xy.example.org///": "https://xy.example.org",
		"  https://xy.example.org ": "https://xy.example.org",
		"":                          "",
	}
	for raw, want := range cases {
		if got := trimPublicURL(raw); got != want {
			t.Errorf("trimPublicURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
