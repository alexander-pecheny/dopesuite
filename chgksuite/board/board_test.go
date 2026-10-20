package board

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseURL(t *testing.T) {
	cases := []struct {
		in      string
		service Service
		host    string
		id      string
		base    string
	}{
		{"https://trello.com/b/3CRjyqFW/blah", Trello, "trello.com", "3CRjyqFW", "https://trello.com"},
		{"trello.com/b/3CRjyqFW", Trello, "trello.com", "3CRjyqFW", "https://trello.com"},
		{"3CRjyqFW", Trello, "trello.com", "3CRjyqFW", "https://trello.com"},
		{"https://xy.pecheny.me/board/2", XY, "xy.pecheny.me", "2", "https://xy.pecheny.me"},
		{"http://localhost:9673/board/7", XY, "localhost:9673", "7", "http://localhost:9673"},
	}
	for _, c := range cases {
		b, err := ParseURL(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if b.Service != c.service || b.Host != c.host || b.ID != c.id || b.BaseURL != c.base {
			t.Errorf("%s → %+v", c.in, b)
		}
	}
	if _, err := ParseURL("https://example.com/nope"); err == nil {
		t.Error("a URL with no board id should be an error")
	}
}

// fix_trello_new_editor_links: the editor turned a bare URL into [url](url).
func TestFixTrelloLinks(t *testing.T) {
	cases := [][2]string{
		{"см. [https://a.example/x](https://a.example/x) там", "см. https://a.example/x там"},
		{"[текст](https://a.example/x)", "[текст](https://a.example/x)"},
		{"без ссылок", "без ссылок"},
	}
	for _, c := range cases {
		if got := fixTrelloLinks(c[0]); got != c[1] {
			t.Errorf("fixTrelloLinks(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestProcessDesc(t *testing.T) {
	desc := "Вопрос?\nОтвет: да\nКомментарий: нет\nЗачёт: ага"
	if got := processDesc(desc, true, false); got != "Ответ: да\nЗачёт: ага" {
		t.Errorf("onlyanswers = %q", got)
	}
	if got := processDesc(desc, false, true); got != "Вопрос?" {
		t.Errorf("noanswers = %q", got)
	}
	if got := processDesc("a \\* b \\` c", false, false); got != "a * b ` c" {
		t.Errorf("escapes = %q", got)
	}
}

func TestCards(t *testing.T) {
	// The author line needs its own newline to be seen, in Python as here.
	src := "? Первый вопрос?\n! Ответ один.\n@ Иванов\n\n\n? Второй?\n! Ответ два.\n"
	got := Cards(src, false)
	if len(got) != 2 || got[0].Caption != "Ответ один" || got[1].Caption != "Ответ два" {
		t.Fatalf("%+v", got)
	}
	// --author only fires when the author is not the card's last line: the
	// split leaves no newline after it, and the pattern wants one. chgksuite
	// does the same, so the captions on a shared board match.
	if got := Cards(src, true)[0].Caption; got != "Ответ один" {
		t.Errorf("author as the last line = %q, want it unchanged", got)
	}
	mid := "? Вопрос?\n! Ответ.\n@ Иванов\n^ Источник.\n"
	if got := Cards(mid, true)[0].Caption; got != "Ответ Иванов" {
		t.Errorf("author above another field = %q", got)
	}
}

func TestDownloadWritesOneFilePerList(t *testing.T) {
	j := &JSON{
		Lists: []List{{ID: "1", Name: "Тур 1"}, {ID: "2", Name: "Тур 2"}, {ID: "3", Name: "Закрытый", Closed: true}},
		Cards: []Card{
			{ID: "a", IDList: "1", Desc: "? Раз?\n! Один."},
			{ID: "b", IDList: "2", Desc: "? Два?\n! Два."},
			{ID: "c", IDList: "3", Desc: "не должен попасть"},
		},
	}
	files, err := Download(j, DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	if !reflect.DeepEqual(names, []string{"Тур 1.4s", "Тур 2.4s"}) {
		t.Fatalf("files = %v", names)
	}
	if got := string(files[0].Data); !strings.Contains(got, "Раз?") {
		t.Errorf("first file = %q", got)
	}
}

func TestDownloadSingleFileFollowsListOrder(t *testing.T) {
	j := &JSON{
		Lists: []List{{ID: "1", Name: "Тур 1"}, {ID: "2", Name: "Тур 2"}},
		Cards: []Card{{ID: "b", IDList: "2", Desc: "второй"}, {ID: "a", IDList: "1", Desc: "первый"}},
	}
	files, err := Download(j, DownloadOptions{SingleFile: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "singlefile.4s" {
		t.Fatalf("files = %+v", files)
	}
	if got := string(files[0].Data); strings.Index(got, "первый") > strings.Index(got, "второй") {
		t.Errorf("lists out of order: %q", got)
	}
}
