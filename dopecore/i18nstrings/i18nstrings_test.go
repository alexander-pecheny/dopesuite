package i18nstrings_test

import (
	"fmt"
	"testing"

	"pecheny.me/dopecore/i18nstrings"
)

func TestParse(t *testing.T) {
	pairs, err := i18nstrings.Parse(`
# a comment
loose = "before any table"

[general]
plain = "text"
escaped = "a\tb\nc\"d"
long = """
first
second"""
literal = 'no \\escapes "here"'

[other]
plain = "elsewhere"
`)
	if err != nil {
		t.Fatal(err)
	}
	want := []i18nstrings.Pair{
		{Table: "", Key: "loose", Value: "before any table", Line: 3},
		{Table: "general", Key: "plain", Value: "text", Line: 6},
		{Table: "general", Key: "escaped", Value: "a\tb\nc\"d", Line: 7},
		{Table: "general", Key: "long", Value: "first\nsecond", Line: 8},
		{Table: "general", Key: "literal", Value: `no \\escapes "here"`, Line: 11},
		{Table: "other", Key: "plain", Value: "elsewhere", Line: 14},
	}
	if len(pairs) != len(want) {
		t.Fatalf("got %d pairs, want %d: %+v", len(pairs), len(want), pairs)
	}
	for i, p := range pairs {
		if p != want[i] {
			t.Errorf("pair %d = %+v, want %+v", i, p, want[i])
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, text := range []string{"[t]\nnot a pair\n", "[t]\nk = bare\n", "[t]\nk = \"\"\"open\n"} {
		if _, err := i18nstrings.Parse(text); err == nil {
			t.Errorf("%q parsed", text)
		}
	}
}

func TestRussianPlural(t *testing.T) {
	for n, want := range map[int]string{
		0: "many", 1: "one", 2: "few", 4: "few", 5: "many", 11: "many", 12: "many",
		14: "many", 15: "many", 20: "many", 21: "one", 22: "few", 24: "few",
		25: "many", 30: "many", 31: "one", 100: "many", 101: "one", 111: "many",
		-1: "one", -3: "few",
	} {
		if got := i18nstrings.Plural("ru", n, "one", "few", "many"); got != want {
			t.Errorf("ru %d = %s, want %s", n, got, want)
		}
	}
}

func TestUnknownLanguageFallsBack(t *testing.T) {
	if got := i18nstrings.Plural("en", 1, "one", "few", "many"); got != "one" {
		t.Errorf("en 1 = %s", got)
	}
	for _, n := range []int{0, 2, 11, 21} {
		if got := i18nstrings.Plural("en", n, "one", "few", "many"); got != "many" {
			t.Errorf("en %d = %s", n, got)
		}
	}
}

func TestRegister(t *testing.T) {
	i18nstrings.Register("zz", func(int) int { return 1 })
	if got := i18nstrings.Plural("zz", 1, "one", "few", "many"); got != "few" {
		t.Errorf("zz = %s", got)
	}
}

func TestUserErrorUnwraps(t *testing.T) {
	err := fmt.Errorf("saving: %w", i18nstrings.User("нельзя"))
	msg, ok := i18nstrings.AsUser(err)
	if !ok || msg != "нельзя" {
		t.Errorf("AsUser = %q, %v", msg, ok)
	}
	if _, ok := i18nstrings.AsUser(fmt.Errorf("plain")); ok {
		t.Error("a plain error passed as a user error")
	}
}
