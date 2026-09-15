package kit

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPageSetCachesOnlyInEmbedMode(t *testing.T) {
	src := fstest.MapFS{"ui/a.dopeui": {Data: []byte("a")}, "ui/bad.dopeui": {Data: []byte("!")}}
	calls := 0
	compile := func(name string, b []byte) ([]byte, error) {
		calls++
		if string(b) == "!" {
			return nil, errors.New("syntax")
		}
		return []byte("<" + string(b) + ">"), nil
	}
	embed := NewPageSet(src, false, compile)
	for range 3 {
		if body, err := embed.Bytes("ui/a.dopeui"); err != nil || string(body) != "<a>" {
			t.Fatalf("got %q %v", body, err)
		}
	}
	if calls != 1 {
		t.Fatalf("embed mode compiled %d times", calls)
	}
	if err := embed.Warm("ui/a.dopeui", "ui/bad.dopeui"); err == nil || !strings.Contains(err.Error(), "ui/bad.dopeui") {
		t.Fatalf("warm err = %v", err)
	}

	calls = 0
	disk := NewPageSet(src, true, compile)
	disk.Bytes("ui/a.dopeui")
	disk.Bytes("ui/a.dopeui")
	if calls != 2 {
		t.Fatalf("disk mode compiled %d times", calls)
	}
	if err := disk.Warm("ui/bad.dopeui"); err != nil {
		t.Fatalf("disk warm compiled: %v", err)
	}
	if _, err := disk.Bytes("ui/missing.dopeui"); err == nil {
		t.Fatal("missing page compiled")
	}
}

func TestLoginPageCompilesUnderCoreAndIsProvided(t *testing.T) {
	src := LoginPage("Вход · test", "/host")
	html, err := Compile("ui/login.dopeui", src)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<title>Вход · test</title>", `data-login-redirect="/host"`, `id="step-password"`, "/static/login.js"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("missing %q", want)
		}
	}
	ps := NewPageSet(fstest.MapFS{"ui/login.dopeui": {Data: []byte("page title=\"app copy\"\n")}}, false, Compile).Provide("ui/login.dopeui", src)
	body, err := ps.Bytes("ui/login.dopeui")
	if err != nil || !strings.Contains(string(body), "Вход · test") {
		t.Fatalf("provided source lost: %v\n%s", err, body)
	}
}

// TestSafeLoginRedirect is the acceptance rule on its own: a destination handed
// to an app on /login?next= is taken only when it is a path on that very site,
// written in characters that cannot escape the attribute it is stamped into.
func TestSafeLoginRedirect(t *testing.T) {
	for _, ok := range []string{
		"/", "/host", "/join/ABC123", "/board/42", "/profile/tokens",
		"/join/x?y=z", "/admin", "/search?q=a%20b&sort=new", "/board/42#card-7",
		"/join/../admin", // same site whatever it normalises to
	} {
		if got := SafeLoginRedirect(ok); got != ok {
			t.Errorf("SafeLoginRedirect(%q) = %q, want it kept", ok, got)
		}
	}
	for _, bad := range []string{
		"",
		"https://evil.example",          // a scheme
		"HTTP://evil.example",           // and in either case
		"//evil.example",                // an authority with the scheme left off
		"/\\evil.example",               // which browsers fold a backslash into
		"javascript:alert(1)",           // not a path at all
		"host/1",                        // relative: would resolve under /login
		` onload="x"`,                   // not a path, and would be markup
		`/x" data-login-redirect="/y`,   // the attribute break the charset stops
		"/café",                         // paths reach an app percent-encoded
		"/x\nSet-Cookie: a=b",           // a newline is not path material
		"/" + strings.Repeat("a", 1024), // longer than any page of ours
	} {
		if got := SafeLoginRedirect(bad); got != "" {
			t.Errorf("SafeLoginRedirect(%q) = %q, want it ignored", bad, got)
		}
	}
	// And what it accepts survives the page it is stamped into.
	html, err := Compile("ui/login.dopeui", LoginPage("t", SafeLoginRedirect("/join/x?y=z")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `data-login-redirect="/join/x?y=z"`) {
		t.Errorf("the destination did not reach the attribute:\n%s", html)
	}
}
