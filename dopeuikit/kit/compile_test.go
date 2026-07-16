package kit

import (
	"strings"
	"testing"
)

func TestCompile_RequiresPageRoot(t *testing.T) {
	if _, err := Compile("t.dopeui", []byte("section\n  hint \"x\"\n")); err == nil {
		t.Fatal("expected an error: top-level node is not a page")
	}
	if _, err := Compile("t.dopeui", []byte("page title=\"T\"\n  section\n")); err != nil {
		t.Fatalf("a page-rooted file should compile: %v", err)
	}
}

// TestCompile_ByteExact locks the printer + core page chrome (DefaultApp).
func TestCompile_ByteExact(t *testing.T) {
	src := `page title="Пример" kind="full"
  topbar title="Привет"
  hint "Вход для " (strong id="who") "."
  message id="m"
`
	want := `<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Пример</title>
  <link rel="preload" href="/static/fonts/noto-sans-var.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/static/styles.css">
  <script src="/static/menu.js"></script>
</head>
<body class="host">
  <header class="host-top">
    <h1>Привет</h1>
    <div class="host-actions">
      <span class="sync-status" id="status" data-state="saved" aria-label="Готово" title="Готово"></span>
    </div>
  </header>
  <main class="match-main">
    <p class="hint">Вход для <strong id="who"></strong>.</p>
    <pre class="message" id="m"></pre>
  </main>
</body>
</html>
`
	got, err := Compile("snippet.dopeui", []byte(src))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if string(got) != want {
		t.Fatalf("output mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestCompile_IconbtnBadgeIdiom(t *testing.T) {
	src := `page title="T" kind="full"
  topbar title="Доска"
    iconbtn id="notifToggle" label="События" badgeid="notifBadge" "🔔"
`
	want := `<button class="action-icon notif-toggle" id="notifToggle" type="button" aria-label="События" title="События">🔔<span class="notif-badge" id="notifBadge" hidden></span></button>`
	got, err := Compile("t.dopeui", []byte(src))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(string(got), want) {
		t.Fatalf("missing badge idiom:\n%s", got)
	}
}
