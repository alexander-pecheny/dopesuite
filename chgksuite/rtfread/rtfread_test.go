package rtfread

import "testing"

func TestToText(t *testing.T) {
	for _, c := range []struct{ name, rtf, want string }{
		{"code page of the font", `{\rtf1\ansi\ansicpg1252{\fonttbl{\f0\fcharset204 Arial;}{\f1\fcharset0 Times;}}\f0 \'d0\'e5\'e4\'e0\'ea\'f2\'ee\'f0\par\f1 caf\'e9}`, "Редактор\ncafé"},
		{"document code page", `{\rtf1\ansi\ansicpg1251 \'c8\'e2\'e0\'ed}`, "Иван"},
		{"unicode with fallback", `{\rtf1\uc1\u1055?\u1105?\uc2\u1090??}`, "Пёт"},
		{"skipped destinations", `{\rtf1{\info{\title Секрет}}{\*\generator Word}{\header Колонтитул}{\field{\*\fldinst HYPERLINK "x"}{\fldrslt ссылка}} текст}`, "ссылка текст"},
		{"escapes and breaks", `{\rtf1 a\{b\}\\c\tab d\line e\~f\-g}`, "a{b}\\c\td\ne fg"},
	} {
		got, err := ToText([]byte(c.rtf))
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if _, err := ToText([]byte("plain")); err != ErrNotRTF {
		t.Errorf("not RTF: %v", err)
	}
}
