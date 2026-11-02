package telegram

import (
	"os"
	"strings"
	"testing"
)

func TestExtractTakesThePostsTextWithItsLineBreaks(t *testing.T) {
	page, err := os.ReadFile("testdata/post.html")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Extract(string(page))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Text, "Вопрос 12\nГероиня Жана Кокто") || !strings.Contains(p.Text, "\nОтвет: стекольщик.\n") {
		t.Errorf("text: %q", p.Text)
	}
	if strings.Contains(p.Text, "<") {
		t.Errorf("a tag survived: %q", p.Text)
	}
}

func TestOnlyAPublicPostIsALink(t *testing.T) {
	for link, want := range map[string]bool{
		"https://t.me/omps2026/30":   true,
		"https://t.me/s/omps2026/30": true,
		"https://t.me/omps2026":      false,
		"https://example.com/a/1":    false,
	} {
		if IsPost(link) != want {
			t.Errorf("IsPost(%q) = %v", link, !want)
		}
	}
	if _, err := Extract("<html></html>"); err != ErrNotPost {
		t.Errorf("an empty page: %v", err)
	}
}
