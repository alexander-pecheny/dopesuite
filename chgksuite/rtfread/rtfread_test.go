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

// A body picture in PNG or JPEG becomes an (img …) directive where it stands;
// Word's WMF fallback, a WMF-only picture and one in a header do not.
func TestToTextImages(t *testing.T) {
	rtf := `{\rtf1\ansi{\header {\pict\pngblip 89504e47}}Вопрос \par` +
		`{\*\shppict{\pict{\*\picprop{\sp{\sn x}{\sv 1}}}{\*\blipuid 0123abcd}\picw10\pngblip 89504e470d0a1a0a}}{\nonshppict{\pict\wmetafile8 0100}}` +
		` и {\pict\wmetafile8 0100}{\pict\jpegblip\bin3 ` + "\xff\xd8\xff" + `}\par Ответ}`
	got, images, err := ToTextImages([]byte(rtf), "p_")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Вопрос \n(img p_image1.png) и (img p_image2.jpg)\nОтвет"; got != want {
		t.Errorf("text %q, want %q", got, want)
	}
	if len(images) != 2 || images[0].Name != "p_image1.png" || string(images[0].Data) != "\x89PNG\r\n\x1a\n" ||
		images[1].Name != "p_image2.jpg" || string(images[1].Data) != "\xff\xd8\xff" {
		t.Errorf("images %q", images)
	}
	if plain, _ := ToText([]byte(rtf)); plain != "Вопрос \n и \nОтвет" {
		t.Errorf("ToText %q", plain)
	}
}
