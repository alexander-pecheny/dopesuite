package credits

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
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

// An .rtf's picture comes with its question, named after the file.
func TestQuestionImagesOfAnRTF(t *testing.T) {
	pic := pngOf(t)
	rtf := `{\rtf1\ansi\ansicpg1251 \'c2\'ee\'ef\'f0\'ee\'f1 1. \'d7\'f2\'ee \'ed\'e0 \'ea\'e0\'f0\'f2\'e8\'ed\'ea\'e5?\par{\*\shppict{\pict\pngblip ` +
		fmt.Sprintf("%x", pic) + `}}\par \'ce\'f2\'e2\'e5\'f2: \'d1\'ee\'e2\'e0\par}`
	q, err := Question("пакет тура.rtf", []byte(rtf), "1", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Images) != 1 || q.Images[0].Name != "пакет_тура_image1.png" || !bytes.Equal(q.Images[0].Data, pic) ||
		!strings.Contains(q.Source, "(img пакет_тура_image1.png)") {
		t.Fatalf("question %+v", q)
	}
}

// A PDF's picture goes after the lowest line ending above it on its page,
// found in pdftotext's text as written; above every line, first on the page.
func TestPDFPicturePlace(t *testing.T) {
	layout := []byte(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "x"><html><body><doc>
<page><flow><block><line yMax="91"><word>Тур</word><word>1</word></line></block>
<block><line yMax="125"><word>Вопрос</word><word>2.</word></line><line yMax="141"><word>Что</word><word>&amp;</word><word>это?</word></line></block>
<block><line yMax="350"><word>Назовите.</word></line></block></flow></page>
<page><flow><block><line yMax="80"><word>Ответ:</word><word>сова</word></line></block></flow></page></doc></body></html>`)
	lines := pdfLines(layout)
	if len(lines[0]) != 4 || lines[0][2].text != "Что & это?" || lines[1][0].yMax != 80 {
		t.Fatalf("lines %+v", lines)
	}
	page := "Тур 1\nВопрос 2.\nЧто & это?\n\nНазовите.\n"
	ends := pdfLineEnds(page, lines[0])
	for _, c := range []struct {
		top  float64
		want string
	}{
		{247, "Тур 1\nВопрос 2.\nЧто & это?"},
		{142, "Тур 1\nВопрос 2.\nЧто & это?"}, // the line reaches a point below the top: still above
		{400, page[:len(page)-1]},
		{10, ""},
	} {
		if got := page[:pdfAnchor(lines[0], ends, c.top)]; got != c.want {
			t.Errorf("top %v: after %q, want %q", c.top, got, c.want)
		}
	}
}

// The whole of it over a packet chgksuite set in PDF (testdata/pictures.pdf:
// a picture in question 2's text, one in question 3's comment). It needs
// poppler's pdftotext and pdftohtml.
func TestQuestionImagesOfAPDF(t *testing.T) {
	for _, bin := range []string{"pdftotext", "pdftohtml"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
	data, err := os.ReadFile("testdata/pictures.pdf")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ number, source string }{
		{"2", "? Что изображено на картинке?\n(img pictures_image1.jpg)\nНазовите это.\n! Сова"},
		{"3", "(img pictures_image2.jpg)"},
	} {
		q, err := Question("pictures.pdf", data, c.number, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(q.Source, c.source) || len(q.Images) != 1 || pictureMIME(q.Images[0].Name) != "image/jpeg" ||
			!bytes.HasPrefix(q.Images[0].Data, []byte("\xff\xd8\xff")) {
			t.Errorf("question %s: %q, %d images", c.number, q.Source, len(q.Images))
		}
	}
	if q, err := Question("pictures.pdf", data, "1", Options{}); err != nil || len(q.Images) != 0 {
		t.Errorf("question 1: %+v, %v", q, err)
	}
}

// The number is the one the packet writes, as the text parser reads it, never
// a count: notes to the narrator and a warm-up (chgksuite's 0) before question
// 1 do not push it to 2, and two tours numbered from 1 each hold two questions
// 1, neither given.
func TestQuestionNumberIsTheWrittenOne(t *testing.T) {
	// As WitchCraft №2 writes it: a numbered note to the narrator that quotes
	// an «Ответ:» is read as a question without a number, then the warm-up
	// (its label on a line of its own) and question 1. Counted, question 1 is 2.
	warmup := doctest.Doc("3. В ответах к вопросам пакета необязательная их часть указана в круглых скобках. Например: «Ответ: (Александр) Пушкин».\r\r" +
		"Разминочный вопрос\r\rТекст разминки.\rОтвет: о\r\rВопрос 1.\r\rТекст первого.\rОтвет: Макбет.\r\rВопрос 2.\r\rТекст второго.\rОтвет: о\r")
	for number, want := range map[string]string{"0": "Текст разминки.", "1": "Текст первого.", "2": "Текст второго."} {
		q, err := Question("p.doc", warmup, number, Options{})
		if err != nil || !strings.HasPrefix(q.Source, "? "+want) {
			t.Errorf("question %s: %+v, %v", number, q, err)
		}
	}
	if _, err := Question("p.doc", warmup, "3", Options{}); !errors.Is(err, ErrNoQuestion) {
		t.Errorf("question 3 of a packet of 0–2: %v", err)
	}

	tours := doctest.Doc("Тур 1\r\rВопрос 1. Первый первого.\rОтвет: о\r\rТур 2\r\rВопрос 1. Первый второго.\rОтвет: о\r\rВопрос 2. Второй второго.\rОтвет: о\r")
	if _, err := Question("p.doc", tours, "1", Options{}); !errors.Is(err, ErrNoQuestion) {
		t.Errorf("two questions 1: %v", err)
	}
	if q, err := Question("p.doc", tours, "2", Options{}); err != nil || !strings.Contains(q.Source, "Второй второго.") {
		t.Errorf("question 2: %+v, %v", q, err)
	}
}
