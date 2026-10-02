package credits

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"xy/internal/chgk/chgkimport"
	"xy/internal/chgk/docread/doctest"
	"xy/internal/chgk/fsource"
)

const packet = `### Кубок чего-то

# Редакторы благодарят всех.

## Тур 1

#EDITOR Редактор — Иван Иванов (Москва)

# Благодарим за тестирование Петра Петрова.

? СЕКРЕТ-вопрос-1
! СЕКРЕТ-ответ-1
/ СЕКРЕТ-комментарий-1

# СЕКРЕТ-после-вопроса

> СЕКРЕТ-раздатка

## Тур 2

#EDITOR Редактор — Анна Аннина

? СЕКРЕТ-вопрос-2
! СЕКРЕТ-ответ-2
`

func read(t *testing.T, name, src string) *Credits {
	t.Helper()
	c, err := Read(name, []byte(src), Options{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestShape(t *testing.T) {
	got := read(t, "p.4s", packet)
	want := &Credits{
		Language:  "ru",
		Questions: 2,
		Tours:     []Tour{{Label: "Тур 1", First: 1, Last: 1}, {Label: "Тур 2", First: 2, Last: 2}},
		Paragraphs: []Paragraph{
			{Text: "Редакторы благодарят всех.", Tour: -1, Next: 1},
			{Text: "Редактор — Иван Иванов (Москва)", Tour: 0, Next: 1, Editor: true},
			{Text: "Благодарим за тестирование Петра Петрова.", Tour: 0, Next: 1},
			{Text: "СЕКРЕТ-после-вопроса", Tour: 0},
			{Text: "Редактор — Анна Аннина", Tour: 1, Next: 2, Editor: true},
		},
		Authors: []Author{},
		Sources: []string{"p.4s"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	for _, s := range []string{"вопрос", "ответ", "комментарий", "раздатка"} {
		if b, _ := json.Marshal(got); strings.Contains(string(b), "СЕКРЕТ-"+s) {
			t.Fatalf("question field %s leaked: %s", s, b)
		}
	}
}

// A block header between questions gets the number of the question after it,
// warm-up questions are not numbered, and authors come with their question.
func TestBlocksWarmupAuthors(t *testing.T) {
	got := read(t, "p.4s", "## Разминка\n\n? р\n! о\n@ Разминочный Автор\n\n## Тур 1\n\n#EDITOR Редактор — А\n\n? в1\n! о\n@ Иван Иванов (Москва)\n\n# Блок 2. Тестировали Б\n\n? в2\n! о\n")
	if got.Questions != 2 || got.Tours[0].First != 0 || !got.Tours[0].Warmup || got.Tours[1].First != 1 || got.Tours[1].Last != 2 {
		t.Fatalf("shape %+v", got)
	}
	if p := got.Paragraphs[1]; p.Text != "Блок 2. Тестировали Б" || p.Tour != 1 || p.Next != 2 {
		t.Fatalf("block paragraph %+v", p)
	}
	if !reflect.DeepEqual(got.Authors, []Author{{Question: 1, Text: "Иван Иванов (Москва)"}}) {
		t.Fatalf("authors %+v", got.Authors)
	}
}

// An old Word .doc packet is read like a .docx, alone or in a zip, and its
// question fields stay out as they do for every format.
func TestDoc(t *testing.T) {
	doc := doctest.Doc("Кубок чего-то\r\rТур 1\rРедактор — Иван Иванов (Москва)\r" +
		"Благодарим за тестирование Петра Петрова.\r\r" +
		"Вопрос 1. СЕКРЕТ-вопрос-1\rОтвет: СЕКРЕТ-ответ-1\rКомментарий: СЕКРЕТ-комментарий-1\rАвтор: Пётр Петров\r\r" +
		"Тур 2\rРедактор — Анна Аннина\r\r" +
		"Вопрос 2. СЕКРЕТ-вопрос-2\rОтвет: СЕКРЕТ-ответ-2\r")
	for name, data := range map[string][]byte{
		"packet.doc": doc,
		"packet.zip": zipOf(t, map[string]string{"old/packet.doc": string(doc), "handouts.doc": "not a doc"}),
	} {
		got, err := Read(name, data, Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var labels []string
		for _, tr := range got.Tours {
			labels = append(labels, fmt.Sprintf("%s:%d-%d", tr.Label, tr.First, tr.Last))
		}
		if want := []string{"Тур 1:1-1", "Тур 2:2-2"}; got.Questions != 2 || !reflect.DeepEqual(labels, want) {
			t.Fatalf("%s: %d questions, tours %v", name, got.Questions, labels)
		}
		var editors []string
		for _, p := range got.Paragraphs {
			if p.Editor {
				editors = append(editors, p.Text)
			}
		}
		if want := []string{"Редактор — Иван Иванов (Москва)", "Редактор — Анна Аннина"}; !reflect.DeepEqual(editors, want) {
			t.Fatalf("%s: editors %v", name, editors)
		}
		if !reflect.DeepEqual(got.Authors, []Author{{Question: 1, Text: "Пётр Петров"}}) {
			t.Fatalf("%s: authors %+v", name, got.Authors)
		}
		if b, _ := json.Marshal(got); strings.Contains(string(b), "СЕКРЕТ") {
			t.Fatalf("%s: question text leaked: %s", name, b)
		}
	}
}

// A Своя игра file beside the packet (themes of questions numbered 10 to 50)
// credits nobody and is not picked from a zip; a ЧГК packet numbering its
// questions straight through 10, 20 … is still read.
func TestSkipsSI(t *testing.T) {
	theme := func(name string) string {
		out := name + "\r"
		for _, n := range []int{10, 20, 30, 40, 50} {
			out += fmt.Sprintf("%d. СЕКРЕТ-вопрос\rОтвет: СЕКРЕТ\r", n)
		}
		return out
	}
	si := doctest.Doc("Командная своя игра\rАвтор всех тем: Анна Аннина\rБлагодарим за тестирование Петра Петрова.\r" + theme("1. Яблочная") + theme("2. Пауки"))
	got, err := Read("ksi.doc", si, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Questions != 0 || len(got.Paragraphs) != 0 {
		t.Fatalf("Своя игра read as a packet: %+v", got)
	}
	var plain strings.Builder
	for n := 1; n <= 12; n++ {
		fmt.Fprintf(&plain, "Вопрос %d. в\rОтвет: о\r", n)
	}
	packet := doctest.Doc("Тур 1\rРедактор — Иван Иванов\r" + plain.String())
	z, err := Read("all.zip", zipOf(t, map[string]string{"ksi.doc": string(si), "packet.doc": string(packet)}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(z.Sources, []string{"packet.doc"}) {
		t.Fatalf("sources %v", z.Sources)
	}
	var chgk strings.Builder
	chgk.WriteString("Тур 1\rРедактор — Иван Иванов\r")
	for n := 1; n <= 60; n++ {
		fmt.Fprintf(&chgk, "%d. Вопрос\rОтвет: ответ\r", n)
	}
	if c, err := Read("p.doc", doctest.Doc(chgk.String()), Options{}); err != nil || c.Questions != 60 {
		t.Fatalf("numbered ЧГК packet: %v %+v", err, c)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

// A zip of one file per tour comes out in tour order, numbered through, and a
// later file's preamble stands in that file's first tour.
func TestZipOrder(t *testing.T) {
	data := zipOf(t, map[string]string{
		"тур 10.4s":     "## Тур 10\n\n#EDITOR Редактор — Десятый\n\n? в\n! о\n",
		"тур 2.4s":      "#EDITOR Редактор — Второй\n\n## Тур 2\n\n? в\n! о\n",
		"тур 1.4s":      "# Весь пакет тестировали\n\n## Тур 1\n\n#EDITOR Редактор — Первый\n\n? в\n! о\n",
		"handouts.pdf":  "%PDF",
		"__MACOSX/x.4s": "## Тур 99\n",
	})
	got, err := Read("packet.zip", data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, tr := range got.Tours {
		labels = append(labels, fmt.Sprintf("%s:%d", tr.Label, tr.First))
	}
	if want := []string{"Тур 1:1", "Тур 2:2", "Тур 10:3"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("tours %v, want %v", labels, want)
	}
	want := []Paragraph{
		{Text: "Весь пакет тестировали", Tour: -1, Next: 1},
		{Text: "Редактор — Первый", Tour: 0, Next: 1, Editor: true},
		{Text: "Редактор — Второй", Tour: 1, Next: 2, Editor: true},
		{Text: "Редактор — Десятый", Tour: 2, Next: 3, Editor: true},
	}
	if !reflect.DeepEqual(got.Paragraphs, want) {
		t.Fatalf("paragraphs %+v", got.Paragraphs)
	}
}

func questions(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "? в%d\n! о\n\n", i)
	}
	return b.String()
}

// A zip usually holds more than the packet: handouts, spare questions, the same
// packet as a PDF. The question count picks the packet; without it, the biggest
// file of the most trusted format wins when it clearly dominates.
func TestPickPacketFromZip(t *testing.T) {
	data := zipOf(t, map[string]string{
		"пакет.4s":    "## Тур 1\n\n#EDITOR Редактор — Настоящий\n\n" + questions(36),
		"запасные.4s": "## Запасные\n\n#EDITOR Редактор — Запасной\n\n" + questions(3),
		"раздатка.4s": "## Тур 1\n\n# Раздаточный материал\n",
	})
	for _, n := range []int{36, 0} {
		got, err := Read("packet.zip", data, Options{Questions: n})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Sources, []string{"пакет.4s"}) {
			t.Fatalf("questions=%d: sources %v", n, got.Sources)
		}
	}
	// One file per tour: 12 + 12 + 12 = 36 takes them all.
	perTour := zipOf(t, map[string]string{
		"1.4s": "## Тур 1\n\n" + questions(12),
		"2.4s": "## Тур 2\n\n" + questions(12),
		"3.4s": "## Тур 3\n\n" + questions(12),
	})
	got, err := Read("packet.zip", perTour, Options{Questions: 36})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 3 {
		t.Fatalf("per-tour: %+v", got)
	}
}

// The same packet in Ukrainian and in Russian: the Russian one is read, though
// the Ukrainian file comes first and has the same tours; the output says so.
func TestZipPrefersRussian(t *testing.T) {
	ua := "# Редактори пакета дякують усім, хто допомагав готувати ці запитання, і особливо тестувальникам за їхню роботу та терпіння.\n\n"
	ru := "# Редакторы пакета благодарят всех, кто помогал готовить эти вопросы, и особенно тестировщиков за их работу и терпение.\n\n"
	data := zipOf(t, map[string]string{
		"1 пакет укр.4s": "## Тур 1\n\n" + strings.Repeat(ua, 3) + "#EDITOR Редактор — Петро Петренко\n\n" + questions(12),
		"2 пакет рус.4s": "## Тур 1\n\n" + strings.Repeat(ru, 3) + "#EDITOR Редактор — Пётр Петренко\n\n" + questions(12),
	})
	for _, n := range []int{12, 0} {
		got, err := Read("packet.zip", data, Options{Questions: n})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Sources, []string{"2 пакет рус.4s"}) || got.Language != "ru" {
			t.Fatalf("questions=%d: sources %v, language %q", n, got.Sources, got.Language)
		}
	}
	only, err := Read("p.4s", []byte("## Тур 1\n\n"+strings.Repeat(ua, 3)+questions(12)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if only.Language != "ua" {
		t.Fatalf("ukrainian file: language %q", only.Language)
	}
}

// A member that can't be unpacked (here an unknown compression method, as a
// PDF packed by an odd archiver) does not hide the packet beside it.
func TestZipSkipsBrokenMember(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "broken.pdf", Method: 99, CompressedSize64: 3, UncompressedSize64: 3})
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("xyz"))
	w, err = zw.Create("packet.4s")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("## Tour 1\n\n" + questions(12)))
	zw.Close()
	got, err := Read("packet.zip", buf.Bytes(), Options{Questions: 12})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Sources, []string{"packet.4s"}) {
		t.Fatalf("sources %v", got.Sources)
	}
}

// The same packet twice in an archive is read once.
func TestZipDuplicatePacket(t *testing.T) {
	body := "## Тур 1\n\n#EDITOR Редактор — А\n\n" + questions(12)
	got, err := Read("packet.zip", zipOf(t, map[string]string{"пакет.4s": body, "пакет для ведущих.4s": body}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tours) != 1 || got.Questions != 12 {
		t.Fatalf("got %+v", got)
	}
}

// A heading with nothing under it before the first real tour does not take
// the preamble's credits with it.
func TestEmptyLeadingHeading(t *testing.T) {
	got := read(t, "p.4s", "## Третий тур\n\n#EDITOR Редакторы — А\n\n## Тур 1\n\n? в\n! о\n")
	if len(got.Tours) != 1 || got.Tours[0].Label != "Тур 1" {
		t.Fatalf("tours %+v", got.Tours)
	}
	if p := got.Paragraphs[0]; p.Tour != -1 || p.Next != 1 {
		t.Fatalf("paragraph %+v", p)
	}
}

// A question before the first tour heading is a warm-up: tours are numbered
// from 1 without it, unless the count already matches the tournament's.
func TestLeadingWarmup(t *testing.T) {
	src := "# Редакторы — А\n\n? разминка\n! о\n@ Разминочный Автор\n\n## Тур 1\n\n#EDITOR Редактор — Б\n\n" + questions(2)
	got, err := Read("p.4s", []byte(src), Options{Questions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got.Questions != 2 || got.Tours[0].First != 1 || got.Tours[0].Last != 2 || len(got.Authors) != 0 || got.Paragraphs[0].Next != 1 {
		t.Fatalf("got %+v", got)
	}
	kept, _ := Read("p.4s", []byte(src), Options{Questions: 3})
	if kept.Questions != 3 || kept.Tours[0].First != 2 {
		t.Fatalf("kept %+v", kept)
	}
}

func TestLanguage(t *testing.T) {
	ua := strings.Repeat("Запитання 1. Цей тур присвячується всім, хто допомагав. Відповідь: кіт. Коментар: дякуємо редакторам за роботу. ", 5)
	ru := strings.Repeat("Вопрос 1. Этот тур посвящается всем, кто помогал. Ответ: кот. Комментарий: благодарим редакторов за работу. ", 5)
	if got := language(ua); got != "ua" {
		t.Fatalf("ukrainian: %q", got)
	}
	if got := language(ru); got != "" {
		t.Fatalf("russian: %q", got)
	}
	if got := language("Вопрос 1"); got != "" {
		t.Fatalf("too short: %q", got)
	}
}

func TestUnwrap(t *testing.T) {
	in := "Тур 1\nРедактор — Иван Иванов (Москва).\nРедактор благодарит за тестирование Петра Петрова, Анну\nАннину и Сидора Сидорова.\nВопрос 1. Длинный вопрос, который переносится на\nследующую строку.\nОтвет: ответ."
	want := "Тур 1\nРедактор — Иван Иванов (Москва).\nРедактор благодарит за тестирование Петра Петрова, Анну Аннину и Сидора Сидорова.\nВопрос 1. Длинный вопрос, который переносится на следующую строку.\nОтвет: ответ."
	if got := unwrap(in); got != want {
		t.Fatalf("got\n%s", got)
	}
}

// Whatever the file holds, the output stays small.
func TestOutputIsCapped(t *testing.T) {
	var b strings.Builder
	for i := range 200 {
		fmt.Fprintf(&b, "## Тур %d\n\n# %s\n\n", i, strings.Repeat("я", 5000))
		for range 5 {
			b.WriteString("# Тестировали Б.\n\n")
		}
		b.WriteString("? в\n! о\n\n")
	}
	got := read(t, "p.4s", b.String())
	if len(got.Tours) != maxTours || len(got.Paragraphs) != maxParagraphs {
		t.Fatalf("tours %d, paragraphs %d", len(got.Tours), len(got.Paragraphs))
	}
	for _, p := range got.Paragraphs {
		if utf8.RuneCountInString(p.Text) > maxParagraph {
			t.Fatalf("paragraph of %d runes", utf8.RuneCountInString(p.Text))
		}
	}
}

func TestNaturalLess(t *testing.T) {
	if !naturalLess("тур 2.docx", "тур 10.docx") || naturalLess("тур 10.docx", "тур 2.docx") {
		t.Fatal("digit runs must compare as numbers")
	}
}

// Over chgksuite's real packets: no question field but the author appears in
// any credits paragraph.
func TestNoQuestionFieldInCredits(t *testing.T) {
	dir := os.Getenv("XY_CHGKSUITE_TESTS")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "chgksuite", "chgksuite", "tests")
	}
	docs, _ := filepath.Glob(filepath.Join(dir, "*.docx"))
	if len(docs) == 0 {
		t.Skipf("chgksuite tests dir not found (%s)", dir)
	}
	for _, p := range docs {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		c, err := Read(filepath.Base(p), data, Options{})
		if err != nil {
			continue
		}
		res, err := chgkimport.Parse(filepath.Base(p), data, "chgk")
		if err != nil {
			continue
		}
		var fields []string
		for _, pair := range fsource.Parse(res.Source, "chgk") {
			q, ok := pair.Content.(*fsource.Question)
			if !ok {
				continue
			}
			for _, k := range q.Keys() {
				if k == "author" || k == "number" || k == "setcounter" {
					continue // authors are credited by name, so they may well recur
				}
				if s := text(q.Get(k)); len([]rune(s)) >= 20 {
					fields = append(fields, s)
				}
			}
		}
		for _, para := range c.Paragraphs {
			for _, f := range fields {
				if strings.Contains(para.Text, f) {
					t.Errorf("%s: paragraph carries question text %q", filepath.Base(p), f)
				}
			}
		}
	}
}
