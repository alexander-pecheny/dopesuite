package main

import "testing"

func TestClassify(t *testing.T) {
	const code = "2TDR7XMG6VEREBQWFUNA"
	cases := []struct {
		name string
		text string
		want intent
	}{
		{"deep-link start carries the code", "/start " + code, intent{kind: intentRegister, code: code}},
		{"deep-link start lowercased code", "/start 2tdr7xmg6verebqwfuna", intent{kind: intentRegister, code: code}},
		{"group start@botname with code", "/start@xy_pecheny_bot " + code, intent{kind: intentRegister, code: code}},
		{"bare start points at site", "/start", intent{kind: intentLogin}},
		{"login points at site", "/login", intent{kind: intentLogin}},
		{"pasted code registers", code, intent{kind: intentRegister, code: code}},
		{"unknown command points at site", "/help", intent{kind: intentLogin}},
		{"empty ignored", "   ", intent{kind: intentIgnore}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.text); got != tc.want {
				t.Fatalf("classify(%q) = %+v, want %+v", tc.text, got, tc.want)
			}
		})
	}
}
