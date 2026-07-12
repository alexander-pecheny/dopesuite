package textparse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/typo"
)

// chgksuiteTests locates chgksuite's own test corpus. Its .canon files are what
// `chgksuite parse` produces and are the parity target for this port.
func chgksuiteTests(t *testing.T) string {
	dir := os.Getenv("XY_CHGKSUITE_TESTS")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "chgksuite", "chgksuite", "tests")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("chgksuite tests dir not found (%s)", dir)
	}
	return dir
}

// txtFixtures are the UTF-8 .txt packages in chgksuite's corpus, which reach the
// parser as plain text — so they test ChgkParser on its own, with no docx reader
// in the way. (bkd_2021_all is game=brain, which still uses ChgkParser.)
//
// balt09-1.txt is deliberately absent: it starts with "Чемпионат:", which
// chgksuite hands to a different parser entirely (parser_db.chgk_parse_db), not
// ChgkParser. xy has no use for that DB-export format. The .docx fixtures are
// covered end-to-end by the chgkimport package.
var txtFixtures = []string{
	"bkd_2021_all.txt",
	"borromeo.txt",
	"test_blitz.txt",
}

// TestCanonParity mirrors chgk_parse_txt: decode, escape underscores outside
// URLs, parse, compose — and require chgksuite's exact output.
func TestCanonParity(t *testing.T) {
	dir := chgksuiteTests(t)
	for _, name := range txtFixtures {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(dir, name+".canon"))
			if err != nil {
				t.Fatal(err)
			}
			text := strings.ReplaceAll(string(raw), "\r\n", "\n")
			text = typo.EscapeUnderscoresExceptURLs(text, false)

			got := fsource.Compose(Parse(text, Options{}), fsource.NumbersDefault)
			if w := strings.ReplaceAll(string(want), "\r\n", "\n"); got != w {
				t.Errorf("mismatch:\n%s", firstDiff(w, got))
			}
		})
	}
}

// firstDiff renders the first differing line with a little context.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl == gl {
			continue
		}
		var b strings.Builder
		for j := max(0, i-3); j < i; j++ {
			b.WriteString("  " + w[j] + "\n")
		}
		b.WriteString("line " + itoa(i+1) + "\n- want: " + wl + "\n+ got:  " + gl + "\n")
		return b.String()
	}
	return "(only trailing differences)"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
