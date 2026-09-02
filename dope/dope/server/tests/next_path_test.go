package tests

import (
	"testing"

	dopeserver "dope/dope/server"
)

func TestSafeNextPathKeepsOnlySameSitePaths(t *testing.T) {
	for next, want := range map[string]string{
		"/reg/tok":                  "/reg/tok",
		"/vote/tok?x=1":             "/vote/tok?x=1",
		"/host/fest/1#access":       "/host/fest/1#access",
		"":                          "",
		"host":                      "",
		"//evil.com":                "",
		`/\evil.com`:                "",
		`/\\evil.com`:               "",
		"https://evil.com":          "",
		"//evil.com/reg":            "",
		"http:/evil.com":            "",
		"javascript:alert(1)":       "",
		"mailto:a@b.c":              "",
		"/reg/tok\r\nSet-Cookie: x": "",
		"/reg/\"tok":                "",
	} {
		if got := dopeserver.SafeNextPath(next); got != want {
			t.Errorf("SafeNextPath(%q) = %q, want %q", next, got, want)
		}
	}
}
