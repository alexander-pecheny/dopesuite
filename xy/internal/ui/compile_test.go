package ui

import (
	"strings"
	"testing"
)

// TestCompile_ByteExact locks down the printer against a small snippet that
// exercises void elements, empty elements, inline text, nested inline
// elements, block nesting, a blank line and a comment.
func TestCompile_ByteExact(t *testing.T) {
	src := `doctype
html lang="ru"
  head
    meta charset="utf-8"
    title "Пример"
  body class="host"
    -- top banner
    header class="host-top"
      h1 "Привет"
      p class="auth-hint" "Вход для " (strong id="who") "."

    div id="empty" class="kanban" hidden
`
	want := `<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <title>Пример</title>
</head>
<body class="host">
  <!-- top banner -->
  <header class="host-top">
    <h1>Привет</h1>
    <p class="auth-hint">Вход для <strong id="who"></strong>.</p>
  </header>

  <div id="empty" class="kanban" hidden></div>
</body>
</html>
`
	got, err := Compile("snippet.xui", []byte(src))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if string(got) != want {
		t.Fatalf("output mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestCompile_MultiLineComment(t *testing.T) {
	src := `doctype
html lang="ru"
  head
  body
    -- move / copy list overlay (within board → re-rank; other board →
    -- client-side re-encryption of the list + its cards)
    div id="x"
`
	want := "  <!-- move / copy list overlay (within board → re-rank; other board →\n" +
		"       client-side re-encryption of the list + its cards) -->\n"
	got, err := Compile("t.xui", []byte(src))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(string(got), want) {
		t.Fatalf("output missing expected comment block:\n--- got ---\n%s\n--- want substring ---\n%s", got, want)
	}
}

func TestCompile_InlineRunLine(t *testing.T) {
	src := `doctype
html
  head
  body
    button id="notifToggle" type="button"
      "🔔" (span class="notif-badge" id="notifBadge" hidden)
`
	want := "  <button id=\"notifToggle\" type=\"button\">\n" +
		"    🔔<span class=\"notif-badge\" id=\"notifBadge\" hidden></span>\n" +
		"  </button>\n"
	got, err := Compile("t.xui", []byte(src))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(string(got), want) {
		t.Fatalf("output mismatch:\n--- got ---\n%s\n--- want substring ---\n%s", got, want)
	}
}
