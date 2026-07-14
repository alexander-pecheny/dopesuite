package markdown

import (
	"strings"
	"testing"
)

func TestRenderMarkdownDetails(t *testing.T) {
	t.Run("basic disclosure with markdown body", func(t *testing.T) {
		src := ":::details Состав судей\nИван **Петров**, Мария Сидорова\n:::"
		got := string(Render(src))
		for _, want := range []string{
			"<details>",
			"<summary>Состав судей</summary>",
			"<strong>Петров</strong>",
			"</details>",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q in:\n%s", want, got)
			}
		}
	})

	t.Run("multi-paragraph and list body", func(t *testing.T) {
		src := ":::details Подробности\nПервый абзац.\n\n- один\n- два\n:::"
		got := string(Render(src))
		if !strings.Contains(got, "<li>один</li>") || !strings.Contains(got, "<p>Первый абзац.</p>") {
			t.Fatalf("body markdown not rendered:\n%s", got)
		}
	})

	t.Run("default summary when label omitted", func(t *testing.T) {
		src := ":::details\nтекст\n:::"
		got := string(Render(src))
		if !strings.Contains(got, "<summary>"+defaultDetailsSummary+"</summary>") {
			t.Fatalf("expected default summary:\n%s", got)
		}
	})

	t.Run("summary is HTML-escaped, not raw HTML", func(t *testing.T) {
		src := ":::details <img src=x onerror=alert(1)>\nтекст\n:::"
		got := string(Render(src))
		if strings.Contains(got, "<img") {
			t.Fatalf("summary raw HTML leaked:\n%s", got)
		}
		if !strings.Contains(got, "&lt;img") {
			t.Fatalf("summary not escaped:\n%s", got)
		}
	})

	t.Run("raw HTML inside body is still dropped", func(t *testing.T) {
		src := ":::details t\n<script>alert(1)</script>\n:::"
		got := string(Render(src))
		if strings.Contains(got, "<script>") {
			t.Fatalf("script tag leaked from body — unsafe HTML enabled?\n%s", got)
		}
	})

	t.Run("interrupts a paragraph", func(t *testing.T) {
		src := "вступление\n:::details Тут\nвнутри\n:::"
		got := string(Render(src))
		if !strings.Contains(got, "<details>") || !strings.Contains(got, "<summary>Тут</summary>") {
			t.Fatalf("disclosure after paragraph not parsed:\n%s", got)
		}
	})

	t.Run("unterminated fence closes at EOF", func(t *testing.T) {
		src := ":::details Тут\nвнутри без закрытия"
		got := string(Render(src))
		if !strings.Contains(got, "<details>") || !strings.Contains(got, "</details>") {
			t.Fatalf("unterminated disclosure not closed at EOF:\n%s", got)
		}
	})

	t.Run("bare colon fence is not a disclosure", func(t *testing.T) {
		src := ":::\nне details\n:::"
		got := string(Render(src))
		if strings.Contains(got, "<details>") {
			t.Fatalf("bare ::: should not open a disclosure:\n%s", got)
		}
	})

	t.Run("plain markdown unaffected", func(t *testing.T) {
		got := string(Render("# Заголовок\n\nабзац с **жирным**"))
		if !strings.Contains(got, "<h1>") || !strings.Contains(got, "<strong>жирным</strong>") {
			t.Fatalf("ordinary markdown broken:\n%s", got)
		}
	})
}
