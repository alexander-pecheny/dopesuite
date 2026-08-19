package tgbridge

import (
	"net/http/httptest"
	"testing"
)

func TestSecretOK(t *testing.T) {
	cases := []struct {
		name, secret, header string
		ok, configured       bool
	}{
		{"unconfigured closes the bridge", "", "", false, false},
		{"unconfigured even with a header", "", "x", false, false},
		{"match", "s3cret", "s3cret", true, true},
		{"mismatch", "s3cret", "S3cret", false, true},
		{"missing header", "s3cret", "", false, true},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/", nil)
		if c.header != "" {
			r.Header.Set("X-Bot-Secret", c.header)
		}
		ok, configured := SecretOK(r, c.secret)
		if ok != c.ok || configured != c.configured {
			t.Errorf("%s: ok=%v configured=%v", c.name, ok, configured)
		}
	}
}

func TestLooksLikeRegisterCode(t *testing.T) {
	cases := map[string]bool{
		"ABCD234567": true, "ABC": false, "abcd2345": false, "ABCD1890": false,
		"A234567B": true, "": false,
	}
	for code, want := range cases {
		if got := LooksLikeRegisterCode(code); got != want {
			t.Errorf("%q: got %v want %v", code, got, want)
		}
	}
}
