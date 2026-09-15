package spliffserver

import "testing"

func TestCheckPublicURL(t *testing.T) {
	cases := []struct {
		prod    bool
		raw     string
		wantErr bool
	}{
		{true, "https://spliff.example.org", false},
		{true, "", true},
		{true, "   ", true},
		{false, "", false},
	}
	for _, c := range cases {
		if err := checkPublicURL(c.prod, c.raw); (err != nil) != c.wantErr {
			t.Errorf("checkPublicURL(%v, %q) = %v, wantErr %v", c.prod, c.raw, err, c.wantErr)
		}
	}
}

func TestTrimPublicURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://spliff.example.org/":  "https://spliff.example.org",
		" https://spliff.example.org ": "https://spliff.example.org",
		"https://spliff.example.org//": "https://spliff.example.org",
		"":                             "",
	} {
		if got := trimPublicURL(raw); got != want {
			t.Errorf("trimPublicURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
