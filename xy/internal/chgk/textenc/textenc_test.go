package textenc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

// TestGuessesRussianEncodings: the same sentence, encoded four ways, has to come
// back as itself. KOI8-R and CP1251 are the pair worth caring about — each
// decodes the other's bytes without complaint, into capitals.
func TestGuessesRussianEncodings(t *testing.T) {
	const want = "Вопрос 1. Что здесь написано? Ответ: ничего особенного, просто русский текст."
	for _, c := range []struct {
		name string
		enc  *charmap.Charmap
	}{
		{"cp1251", charmap.Windows1251},
		{"koi8-r", charmap.KOI8R},
		{"cp866", charmap.CodePage866},
		{"iso8859-5", charmap.ISO8859_5},
	} {
		raw, _, err := transform.Bytes(c.enc.NewEncoder(), []byte(want))
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(raw, "")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != want {
			t.Errorf("%s decoded as %q", c.name, got)
		}
		if forced, err := Decode(raw, c.name); err != nil || forced != want {
			t.Errorf("%s forced: %q (%v)", c.name, forced, err)
		}
	}
	if got, err := Decode([]byte(want), ""); err != nil || got != want {
		t.Errorf("utf-8: %q (%v)", got, err)
	}
}

// TestFixesDoubledLineEndings: a file round-tripped through a Windows editor
// carries "\r\r\n", which would otherwise leave a stray blank line between every
// two lines of the package.
func TestFixesDoubledLineEndings(t *testing.T) {
	got, err := Decode([]byte("первая\r\r\nвторая\r\nтретья\rчетвёртая"), "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "первая\nвторая\nтретья\nчетвёртая" {
		t.Errorf("got %q", got)
	}
}

// TestKOI8Fixture is the real thing: chgksuite's corpus has one KOI8-R package.
func TestKOI8Fixture(t *testing.T) {
	dir := os.Getenv("XY_CHGKSUITE_TESTS")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "chgksuite", "chgksuite", "tests")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "balt09-1.txt"))
	if err != nil {
		t.Skipf("no chgksuite corpus: %v", err)
	}
	text, err := Decode(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "Чемпионат:") {
		t.Errorf("decoded to %q…", text[:min(40, len(text))])
	}
}

// TestRefusesGibberish: bytes that are no encoding of Russian are refused, not
// guessed at — a wrong guess turns a package into mojibake silently.
func TestRefusesGibberish(t *testing.T) {
	if _, err := Decode([]byte{0x81, 0x8D, 0x8F, 0x90, 0x98, 0x9D}, ""); err == nil {
		t.Error("gibberish accepted")
	}
}
