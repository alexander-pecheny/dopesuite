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

// A Cyrillic list name used to arrive as "Ð_Ð°Ð¿Ð°Ñ_.docx": a quoted-string
// filename is latin-1, so raw UTF-8 in it is mojibake. RFC 6266 filename* fixes
// it, and the ASCII filename= stays as the fallback older clients read.
func TestContentDisposition(t *testing.T) {
	cases := map[string]string{
		"normal.docx": `attachment; filename="normal.docx"`,
		"Запас.docx":  `attachment; filename="Zapas.docx"; filename*=UTF-8''%D0%97%D0%B0%D0%BF%D0%B0%D1%81.docx`,
		"Тур 1.pdf":   `attachment; filename="Tur 1.pdf"; filename*=UTF-8''%D0%A2%D1%83%D1%80%201.pdf`,
		"Шах.docx":    `attachment; filename="Shakh.docx"; filename*=UTF-8''%D0%A8%D0%B0%D1%85.docx`,
	}
	for in, want := range cases {
		if got := contentDisposition(in); got != want {
			t.Errorf("contentDisposition(%q) =\n %q, want\n %q", in, got, want)
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

func TestTranslit(t *testing.T) {
	cases := map[string]string{
		"Первый бой":     "Pervyy boy",
		"Раунд «Хамса»":  "Raund Khamsa",
		"ТЗ":             "TZ",
		"ЩИ и Щи":        "SHCHI i Shchi",
		"Платки́":        "Platki",
		"Тур 1 — финал":  "Tur 1 - final",
		"Ёлка, Жук, Юла": "Yolka, Zhuk, Yula",
		"tour 😀":         "tour _",
	}
	for in, want := range cases {
		if got := translit(in); got != want {
			t.Errorf("translit(%q) = %q, want %q", in, got, want)
		}
	}
}
