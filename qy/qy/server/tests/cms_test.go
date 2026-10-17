package tests

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A slice of the old site, in the shapes its generator left.
func archiveFixture(t *testing.T) string {
	root := t.TempDir()
	content := filepath.Join(root, "site", "content")
	writeFile(t, filepath.Join(content, "_index.html"), "---\ntitle: Вопрос года\n---\n<h1>Вопрос года</h1>\n<p><a href=\"2025\">2025</a></p>\n")
	writeFile(t, filepath.Join(content, "2025.md"), "---\ntitle: Вопрос года — 2025\n---\n# Вопрос года — 2025\n\n- [Результаты](results)\n")
	writeFile(t, filepath.Join(content, "2025", "results.html"), "---\ntitle: Итоги\n---\n<script>alert(1)</script>\n<p><b id=\"QY7\">Вопрос QY7.</b> Текст</p>\n{{md}}**жирно**{{/md}}\n")
	writeFile(t, filepath.Join(content, "2025", "rules.md"), `---
ctx:
    year: '2025'
    date_nomination_start: '10 ноября'
    dates_nomination: 'с 10 по 24 ноября'
    date_results: '26 января'
    jury: 'Анна Свирина и Надежда Чеботкова'
title: Вопрос года — 2025. Регламент
---

# Вопрос года — 2025. Регламент

Конкурс «Вопрос года — 2025» проводится с 10 ноября до 26 января 2025 года. Номинация идёт с 10 по 24 ноября. В жюри входят Анна Свирина и Надежда Чеботкова.
`)
	writeFile(t, filepath.Join(root, "site", "static", "img", "merchik.jpg"), "\xff\xd8\xff")
	return root
}

func TestTheArchiveImportsIntoTheCMS(t *testing.T) {
	w := newWorld(t)
	pages, files := w.ts.ImportArchive(archiveFixture(t))
	if pages != 5 || files != 1 {
		t.Fatalf("imported %d pages and %d files", pages, files)
	}
	anon := w.ts.Anonymous()
	if body := w.get(anon, "/", http.StatusOK); !strings.Contains(body, `<a href="2025">2025</a>`) {
		t.Error("the home page is not the archive's index")
	}
	if res := anon.Get("/2025/results"); res.Code != http.StatusMovedPermanently || res.Header().Get("Location") != "/2025/results/" {
		t.Errorf("a page should live at its path with a slash, as on the old site: %d %s", res.Code, res.Header().Get("Location"))
	}
	results := w.get(anon, "/2025/results/", http.StatusOK)
	if strings.Contains(results, "alert(1)") || !strings.Contains(results, `id="QY7"`) || !strings.Contains(results, "<strong>жирно</strong>") {
		t.Errorf("the archived results page came out wrong:\n%s", results)
	}
	if res := anon.Get("/img/merchik.jpg"); res.Code != http.StatusOK || res.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("an archived picture: %d %s", res.Code, res.Header().Get("Content-Type"))
	}
}

func TestNextYearsRulesCarryTheTextWithNewDates(t *testing.T) {
	w := newWorld(t)
	w.ts.ImportArchive(archiveFixture(t))
	w.post(w.admin, "/admin/contest/2026/jurors", url.Values{"name": {"Анна Свирина"}, "telegram": {"a"}, "kind": {"expert"}}, http.StatusSeeOther)
	w.post(w.admin, "/admin/contest/2026/jurors", url.Values{"name": {"Иван Иванов"}, "telegram": {"b"}, "kind": {"expert"}}, http.StatusSeeOther)
	rules := w.get(w.ts.Anonymous(), "/2026/rules/", http.StatusOK)
	for _, want := range []string{
		"Вопрос года — 2026. Регламент",
		"проводится с 9 ноября до 25 января 2027 года",
		"Номинация идёт с 9 по 23 ноября",
		"Анна Свирина и Иван Иванов",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("the 2026 rules lack %q", want)
		}
	}
	if old := w.get(w.ts.Anonymous(), "/2025/rules/", http.StatusOK); !strings.Contains(old, "с 10 по 24 ноября") {
		t.Error("the 2025 rules should stay as they were")
	}
}

func TestAnAdminEditsAPageAndRestoresIt(t *testing.T) {
	w := newWorld(t)
	save := func(body, base, original string, want int) {
		w.post(w.admin, "/admin/pages/save", url.Values{
			"path": {"2026/stories"}, "title": {"Истории {{year}}"}, "format": {"markdown"}, "body": {body},
			"base_updated_at": {base}, "original_path": {original},
		}, want)
	}
	save("# Первая версия {{year}}", "", "", http.StatusSeeOther)
	if body := w.get(w.ts.Anonymous(), "/2026/stories/", http.StatusOK); !strings.Contains(body, "Первая версия 2026") || !strings.Contains(body, "Истории 2026") {
		t.Error("a page under its year should fill in the variables")
	}
	var updated string
	w.ts.DB().QueryRow(`select updated_at from pages where path = '2026/stories'`).Scan(&updated)
	save("# Вторая версия", updated, "2026/stories", http.StatusSeeOther)
	// A save made against the version before is somebody else's edit lost.
	save("# Третья версия", "1999-01-01T00:00:00Z", "2026/stories", http.StatusConflict)
	if n := w.count(`select count(*) from page_revisions where path = '2026/stories'`); n != 2 {
		t.Errorf("revisions: %d", n)
	}
	var first string
	w.ts.DB().QueryRow(`select id from page_revisions where path = '2026/stories' order by id limit 1`).Scan(&first)
	if body := w.get(w.admin, "/admin/pages/history?path=2026/stories&rev="+first, http.StatusOK); !strings.Contains(body, "diff-add") {
		t.Error("the history does not show what the revision changed")
	}
	if body := w.post(w.admin, "/admin/pages/restore", url.Values{"rev": {first}}, http.StatusOK); !strings.Contains(body, "# Первая версия") {
		t.Error("restore should open the old text in the editor")
	}
	w.post(w.admin, "/admin/pages/save", url.Values{"path": {"vote"}, "title": {"x"}, "format": {"markdown"}, "body": {"x"}}, http.StatusBadRequest)
	if res := w.ts.Anonymous().Get("/admin/pages"); res.Code != http.StatusSeeOther {
		t.Errorf("the CMS is open to anybody: %d", res.Code)
	}
}
