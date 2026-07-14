package server

import "testing"

func TestHeaderSafeName(t *testing.T) {
	cases := map[string]string{
		"normal.docx": "normal.docx",
		`a"b`:         "ab",    // quote stripped (Content-Disposition break-out)
		`a\b`:         "ab",    // backslash stripped
		"a\tb\nc":     "abc",   // control bytes stripped
		"тур-1":       "тур-1", // unicode preserved
		"a\x7fb":      "ab",    // DEL stripped
		`"; x="`:      "; x=",  // attempted header param injection neutralized
	}
	for in, want := range cases {
		if got := headerSafeName(in); got != want {
			t.Errorf("headerSafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeImageNameRejectsTraversal(t *testing.T) {
	for _, in := range []string{"../etc/passwd", "..", ".", "", "a/b", `a\b`, "/abs"} {
		if got := safeImageName(in); got != "" && (got == ".." || got == "." || containsSep(got)) {
			t.Errorf("safeImageName(%q) = %q leaked a path", in, got)
		}
	}
}

func containsSep(s string) bool {
	for _, r := range s {
		if r == '/' || r == '\\' {
			return true
		}
	}
	return false
}
