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
