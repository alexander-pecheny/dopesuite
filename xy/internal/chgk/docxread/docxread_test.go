package docxread

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chgksuiteTests is where chgksuite's own fixtures live; oracleDir holds the
// text (NAME.dtxt) and images chgksuite's docx_to_text produced for each of
// them. Both are outside the repo, so the parity tests skip when they are gone.
var (
	chgksuiteTests = envOr("XY_CHGKSUITE_TESTS", "/home/pecheny/chgksuite/chgksuite/tests")
	oracleDir      = envOr("XY_DOCXREAD_ORACLE", "/tmp/claude-1000/-home-pecheny-xy/459d9a4a-2779-4fa6-b052-588f6164496d/scratchpad/oracle")
)

func envOr(key, deflt string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return deflt
}

// The chgk-relevant docx fixtures. octo2021 is the only one chgksuite parses
// with preserve_formatting on.
var fixtures = []struct {
	name               string
	preserveFormatting bool
}{
	{"2019-07-23_beln19_u.docx", false},
	{"Kubok_knyagini_Olgi-2015.docx", false},
	{"Shkolny_Chemp_Estonii-2014_(48v).docx", false},
	{"link_unwrap.docx", false},
	{"malkin_papkov_synchr.docx", false},
	{"octo2021_khmelkov.docx", true},
	{"ovsch_boronenko_3.docx", false},
	{"pass1.docx", false},
	{"single_number_line_test.docx", false},
	{"source_numbering_bug.docx", false},
	{"test_amper.docx", false},
	{"tourrev_with_razmin.docx", false},
	{"СЧР_Шередега_Ермишкин.docx", false},
}

func TestToTextParity(t *testing.T) {
	if _, err := os.Stat(chgksuiteTests); err != nil {
		t.Skipf("chgksuite fixtures not available (%v)", err)
	}
	if _, err := os.Stat(oracleDir); err != nil {
		t.Skipf("oracle dumps not available (%v)", err)
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			docx, err := os.ReadFile(filepath.Join(chgksuiteTests, fx.name))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(oracleDir, fx.name+".dtxt"))
			if err != nil {
				t.Fatal(err)
			}
			// docx_to_text's bn_for_img.
			prefix := strings.ReplaceAll(strings.TrimSuffix(fx.name, ".docx"), " ", "_") + "_"
			got, images, err := ToText(docx, Options{
				PreserveFormatting: fx.preserveFormatting,
				ImagePrefix:        prefix,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("text mismatch:\n%s", firstDiff(got, string(want)))
			}
			checkImages(t, images)
		})
	}
}

// checkImages compares the extracted images against the ones chgksuite wrote
// next to the fixture during the oracle run.
func checkImages(t *testing.T, images []Image) {
	t.Helper()
	for _, img := range images {
		want, err := os.ReadFile(filepath.Join(oracleDir, img.Name))
		if err != nil {
			t.Errorf("image %s: no oracle counterpart: %v", img.Name, err)
			continue
		}
		if !bytes.Equal(img.Data, want) {
			t.Errorf("image %s: %d bytes, oracle has %d", img.Name, len(img.Data), len(want))
		}
	}
}

func firstDiff(got, want string) string {
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gl) || i < len(wl); i++ {
		g, w := at(gl, i), at(wl, i)
		if g == w {
			continue
		}
		var b strings.Builder
		for j := max(0, i-3); j < i; j++ {
			b.WriteString("  ctx  " + quote(at(gl, j)) + "\n")
		}
		b.WriteString("  got  " + quote(g) + "\n")
		b.WriteString("  want " + quote(w) + "\n")
		for j := i + 1; j < min(len(wl), i+4); j++ {
			b.WriteString("  next " + quote(at(wl, j)) + "\n")
		}
		return "line " + itoa(i+1) + " of " + itoa(len(gl)) + " (oracle has " + itoa(len(wl)) + "):\n" + b.String()
	}
	return "lines identical but strings differ (trailing bytes?)"
}

func at(lines []string, i int) string {
	if i < 0 || i >= len(lines) {
		return "<eof>"
	}
	return lines[i]
}

func quote(s string) string {
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return strings.NewReplacer("\t", "\\t", "\r", "\\r").Replace(s)
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
