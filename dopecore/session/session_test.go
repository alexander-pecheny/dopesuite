package session

import "testing"

func TestSecureCookiesFailsSafe(t *testing.T) {
	cases := map[string]bool{
		"": true, "production": true, "prod": true, "staging": true, "Development ": false,
		"development": false, "dev": false, "local": false, "test": false, "testing": false,
	}
	for val, want := range cases {
		t.Setenv(ProdEnvVar, val)
		if got := SecureCookies(); got != want {
			t.Errorf("%s=%q: secure=%v want %v", ProdEnvVar, val, got, want)
		}
	}
}
