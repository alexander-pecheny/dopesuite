package qyserver

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"qy/qy/platform/markdown"
)

// The archive: the static site the old scripts generated (vopros_goda's
// site/content and site/static/img), imported into the CMS so that every past
// year lives in the database next to the new one.
//
//	QY_DB=… qy-server import-archive <vopros_goda checkout>

func runImportArchive(args []string) {
	if len(args) != 1 {
		log.Fatal("usage: qy-server import-archive <vopros_goda checkout>")
	}
	path := os.Getenv("QY_DB")
	if path == "" {
		path = dbFile
	}
	db, err := openDB(path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	s := &server{db: db, now: time.Now}
	pages, files, err := s.importArchive(context.Background(), args[0])
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("imported %d pages and %d files into %s", pages, files, path)
}

func (s *server) importArchive(ctx context.Context, root string) (int, int, error) {
	pages, next, err := readArchivePages(filepath.Join(root, "site", "content"))
	if err != nil {
		return 0, 0, err
	}
	files, err := readArchiveFiles(filepath.Join(root, "site", "static", "img"))
	if err != nil {
		return 0, 0, err
	}
	now := s.stamp()
	err = s.withWriteTx(ctx, "import-archive", func(ctx context.Context, tx *sql.Tx) error {
		for _, p := range pages {
			if err := writePage(ctx, tx, p, now, 0); err != nil {
				return err
			}
		}
		// Next year's rules are a starting point: once they exist, they are
		// the admins' to edit and a second import leaves them be.
		for _, p := range next {
			if err := writeNewPage(ctx, tx, p, now); err != nil {
				return err
			}
		}
		for name, data := range files {
			if err := saveAsset(ctx, tx, "img/"+name, data, now, 0); err != nil {
				return err
			}
		}
		return nil
	})
	return len(pages) + len(next), len(files), err
}

func writeNewPage(ctx context.Context, tx *sql.Tx, p page, now string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `select count(*) from pages where path = ?`, p.Path).Scan(&n); err != nil || n > 0 {
		return err
	}
	return writePage(ctx, tx, p, now, 0)
}

var (
	frontMatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n?`)
	titleRe       = regexp.MustCompile(`(?m)^title:\s*(.*?)\s*$`)
	ctxValueRe    = regexp.MustCompile(`(?m)^    ([a-z_]+): '(.*)'$`)
	scriptRe      = regexp.MustCompile(`(?s)<script\b.*?</script>`)
	mdBlockRe     = regexp.MustCompile(`(?s)\{\{md\}\}(.*?)\{\{/md\}\}`)
)

// readArchivePages reads every page of the old site, and the next year's
// rules made from the last year's. Inline scripts are dropped because the CSP
// would refuse them; web/ts/forms.ts wires the one button they drove.
func readArchivePages(dir string) ([]page, []page, error) {
	var out, next []page
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := filepath.Ext(p)
		// A dot-file is an editor's or macOS's (._name next to every file a
		// mac archived), never a page.
		if (ext != ".md" && ext != ".html") || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, strings.TrimSuffix(p, ext))
		rel = filepath.ToSlash(rel)
		if rel == "_index" {
			rel = ""
		}
		pg := archivePage(rel, ext, string(raw))
		out = append(out, pg)
		if tmpl, ok := nextRules(rel, string(raw), pg); ok {
			next = append(next, tmpl)
		}
		return nil
	})
	return out, next, err
}

func archivePage(rel, ext, raw string) page {
	pg := page{Path: rel, Format: formatMarkdown}
	body := raw
	if m := frontMatterRe.FindStringSubmatch(raw); m != nil {
		body = raw[len(m[0]):]
		if t := titleRe.FindStringSubmatch(m[1]); t != nil {
			pg.Title = t[1]
		}
	}
	if pg.Title == "" {
		pg.Title = "/" + rel
	}
	if ext == ".html" {
		pg.Format = formatHTML
		body = scriptRe.ReplaceAllString(body, "")
		body = mdBlockRe.ReplaceAllStringFunc(body, func(m string) string {
			out, err := markdown.Render(mdBlockRe.FindStringSubmatch(m)[1])
			if err != nil {
				return m
			}
			return out
		})
	}
	pg.Body = strings.TrimSpace(body) + "\n"
	return pg
}

// nextRules makes the following year's rules out of a year's rules page:
// each date and name its front matter recorded is put back as the variable it
// came from, so the text carries over and the dates follow the contest.
func nextRules(rel, raw string, pg page) (page, bool) {
	y, err := strconv.Atoi(strings.TrimSuffix(rel, "/rules"))
	if err != nil || !strings.HasSuffix(rel, "/rules") {
		return page{}, false
	}
	m := frontMatterRe.FindStringSubmatch(raw)
	if m == nil {
		return page{}, false
	}
	vals := ctxValueRe.FindAllStringSubmatch(m[1], -1)
	if len(vals) == 0 {
		return page{}, false
	}
	// Longest first, so a date inside a range is not replaced on its own.
	slices.SortFunc(vals, func(a, b []string) int { return len(b[2]) - len(a[2]) })
	pairs := make([]string, 0, 2*len(vals))
	for _, v := range vals {
		pairs = append(pairs, v[2], "{{"+v[1]+"}}")
	}
	r := strings.NewReplacer(pairs...)
	body, title := r.Replace(pg.Body), r.Replace(pg.Title)
	// The rules say the contest runs until a date "of" a year, which is the
	// year the results come out in.
	body = strings.ReplaceAll(body, "{{date_results}} {{year}}", "{{date_results}} {{results_year}}")
	return page{Path: strconv.Itoa(y+1) + "/rules", Title: title, Format: pg.Format, Body: body}, true
}

func readArchiveFiles(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[e.Name()] = data
	}
	return out, nil
}

//go:embed seed/year.md
var yearPageSeed string

// seedYearPages gives a new contest its year page, and its rules copied from
// the year before, unless they are there already. Rules written with
// variables come out right for the new dates as they are.
func seedYearPages(ctx context.Context, tx *sql.Tx, year int, now string) error {
	y := strconv.Itoa(year)
	var n int
	if err := tx.QueryRowContext(ctx, `select count(*) from pages where path = ?`, y).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if err := writePage(ctx, tx, page{Path: y, Title: str.Contest.Name(y), Format: formatMarkdown, Body: yearPageSeed}, now, 0); err != nil {
			return err
		}
	}
	if err := tx.QueryRowContext(ctx, `select count(*) from pages where path = ?`, y+"/rules").Scan(&n); err != nil || n > 0 {
		return err
	}
	var prev page
	err := tx.QueryRowContext(ctx, `select title, format, body from pages where path = ?`, strconv.Itoa(year-1)+"/rules").
		Scan(&prev.Title, &prev.Format, &prev.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	prev.Path = y + "/rules"
	return writePage(ctx, tx, prev, now, 0)
}
