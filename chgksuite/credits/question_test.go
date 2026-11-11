package credits

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/docread/doctest"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/docx"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/fsource"
)

// A packet of the second tour alone, numbered as the tournament numbers it.
var tourTwo = doctest.Doc("Тур 2\rРедактор — Анна Аннина\r\r" +
	"Вопрос 13. Текст тринадцатого.\rОтвет: Ответ-13.\rЗачёт: Зачёт-13.\rКомментарий: Комментарий-13.\rИсточник: Источник-13.\rАвтор: Пётр Петров\r\r" +
	"Вопрос 14. Текст четырнадцатого.\rОтвет: Ответ-14.\r")

// The question is found by the number the packet writes, not by its place:
// the first question of this file is the 13th.
func TestQuestionByWrittenNumber(t *testing.T) {
	for name, data := range map[string][]byte{
		"tour2.doc": tourTwo,
		"tour2.zip": zipOf(t, map[string]string{"tour2.doc": string(tourTwo)}),
	} {
		q, err := Question(name, data, "13", Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, want := range []string{"? Текст тринадцатого.", "! Ответ-13.", "= Зачёт-13.", "/ Комментарий-13.", "^ Источник-13.", "@ Пётр Петров"} {
			if !strings.Contains(q.Source, want) {
				t.Fatalf("%s: %q not in\n%s", name, want, q.Source)
			}
		}
		if strings.Contains(q.Source, "четырнадцатого") || q.Number != "13" || q.Language != "ru" {
			t.Fatalf("%s: %+v", name, q)
		}
		if _, err := Question(name, data, "1", Options{}); !errors.Is(err, ErrNoQuestion) {
			t.Fatalf("%s: question 1 of a file starting at 13: %v", name, err)
		}
	}
}

// A packet sent as one file per tour is searched file by file: the number is
// the one each file writes. Two files that both start at 1 hold two questions
// 1, and neither is given.
func TestQuestionInAPacketOfOneFilePerTour(t *testing.T) {
	tour := func(label, first, second string) string {
		return string(doctest.Doc(label + "\r\rВопрос " + first + ". Текст-" + first + ".\rОтвет: о\r\rВопрос " + second + ". Текст-" + second + ".\rОтвет: о\r"))
	}
	split := zipOf(t, map[string]string{"t1.doc": tour("Тур 1", "1", "2"), "t2.doc": tour("Тур 2", "3", "4")})
	q, err := Question("p.zip", split, "3", Options{Questions: 4})
	if err != nil || !strings.Contains(q.Source, "Текст-3.") || q.File != "t2.doc" {
		t.Fatalf("question 3: %+v, %v", q, err)
	}
	twice := zipOf(t, map[string]string{"t1.doc": tour("Тур 1", "1", "2"), "t2.doc": tour("Тур 2", "1", "2")})
	if _, err := Question("p.zip", twice, "1", Options{Questions: 4}); !errors.Is(err, ErrNoQuestion) {
		t.Fatalf("two questions 1: %v", err)
	}
}

// pngOf is a small picture.
func pngOf(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// A .docx carries its pictures: the question gives the ones it names, under
// the names its (img …) directives use.
func TestQuestionImagesOfADocx(t *testing.T) {
	src := "? Что на картинке?\n(img картинка.png)\n! Сова\n\n? Второй (img другая.png)\n! о\n"
	data, err := docx.Export(fsource.Parse(src, "chgk"), map[string][]byte{"картинка.png": pngOf(t), "другая.png": pngOf(t)}, docx.Options{})
	if err != nil {
		t.Fatal(err)
	}
	q, err := Question("packet.docx", data, "1", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Images) != 1 || len(q.Images[0].Data) == 0 || !strings.Contains(q.Source, "(img "+q.Images[0].Name) || !strings.HasPrefix(q.Images[0].MIME, "image/") {
		t.Fatalf("images %+v of\n%s", q.Images, q.Source)
	}
}

// A .4s in an archive names pictures beside it: raster ones are given, an SVG
// (which may carry script) and a picture the archive lacks are not.
func TestQuestionImagesBesideA4sInAnArchive(t *testing.T) {
	pic := pngOf(t)
	data := zipOf(t, map[string]string{
		"packet.4s":    "? Вопрос (img a.png) (img b.svg) (img нет.png)\n! о\n",
		"images/a.png": string(pic),
		"b.svg":        "<svg onload=\"alert(1)\"/>",
	})
	q, err := Question("p.zip", data, "1", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Images) != 1 || q.Images[0].Name != "a.png" || q.Images[0].MIME != "image/png" || !bytes.Equal(q.Images[0].Data, pic) {
		t.Fatalf("images %+v", q.Images)
	}
}
