package main

import (
	"path/filepath"
	"testing"
)

func TestServerURL(t *testing.T) {
	for address, want := range map[string]string{
		"xy.pecheny.me":                 "https://xy.pecheny.me/",
		" https://xy.pecheny.me/b/42 ":  "https://xy.pecheny.me/",
		"http://localhost:8080":         "http://localhost:8080/",
		"http://127.0.0.1:9674/boards":  "http://127.0.0.1:9674/",
		"http://xy.pecheny.me":          "",
		"ftp://xy.pecheny.me":           "",
		"https://":                      "",
		"http://localhost.evil.example": "",
	} {
		u, err := serverURL(address)
		got := ""
		if err == nil {
			got = u.String()
		}
		if got != want {
			t.Errorf("serverURL(%q) = %q, %v; want %q", address, got, err, want)
		}
	}
}

func TestServersSurviveReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	s, err := openShell(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Add("a.example")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Add("b.example")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatalf("two tabs share the id %q", a.ID)
	}
	if err := s.Remove(a.ID); err != nil {
		t.Fatal(err)
	}
	s, err = openShell(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	tabs := s.Tabs()
	if len(tabs) != 2 || tabs[0] != localTab || tabs[1] != b {
		t.Fatalf("reopened tabs = %+v, want Local and %+v", tabs, b)
	}
}
