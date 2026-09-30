// Package credits reads who made a packet, for a site that credits the
// people behind each question while the questions are still secret.
//
// It returns the packet's shape (tours and their question numbers), every
// credits paragraph — the meta and editor-line paragraphs, wherever they stand —
// with its position, and each question's «Автор:» field. No other question
// field is ever read. The site turns this into player links and question
// numbers; none of the text is published.
package credits

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pemistahl/lingua-go"

	"xy/internal/chgk/chgkimport"
	"xy/internal/chgk/fsource"
)

// Tour is one tour heading of the packet and the questions under it, numbered
// through the whole packet from 1. A tour with no questions has First 0.
type Tour struct {
	Label  string `json:"label"`
	First  int    `json:"first"`
	Last   int    `json:"last"`
	Warmup bool   `json:"warmup,omitempty"`
}

// Paragraph is one credits paragraph. Tour is the index of the tour it stands
// in, -1 for the preamble; Next is the number of the first question after it
// (0 when none follows in its tour), so a paragraph in a tour's header has
// Next == that tour's First and one between questions 4 and 5 has Next == 5.
// Editor marks the typed editor line («#EDITOR»).
type Paragraph struct {
	Text   string `json:"text"`
	Tour   int    `json:"tour"`
	Next   int    `json:"next"`
	Editor bool   `json:"editor,omitempty"`
}

// Author is a question's «Автор:» field as written.
type Author struct {
	Question int    `json:"question"`
	Text     string `json:"text"`
}

// Credits is what a packet says about its people. Questions counts the
// numbered questions (warm-up ones are not numbered). Sources are the files
// read — inside a zip, the ones picked as the packet.
type Credits struct {
	Questions  int         `json:"questions"`
	Tours      []Tour      `json:"tours"`
	Paragraphs []Paragraph `json:"paragraphs"`
	Authors    []Author    `json:"authors"`
	Sources    []string    `json:"sources"`
}

// Options tune the reading.
type Options struct {
	// Questions is how many questions the tournament has; 0 when unknown. In a
	// zip it picks the packet out of the other documents (handouts, spare
	// questions, an answer sheet): the file with exactly that many questions, or
	// the files of one format that add up to it.
	Questions int
	// PDFToText is the pdftotext binary (poppler) PDFs are read with; empty
	// means "pdftotext" on PATH. With none available, PDFs are skipped.
	PDFToText string
}

// maxInflated bounds the total uncompressed size of the documents read out of a
// zip — the same 150 MB the rating site allows for an upload, so a small archive
// that inflates far past it (a zip bomb) is refused as soon as it crosses.
const maxInflated = 150 << 20

// Read extracts the credits of one uploaded file. The name's extension picks the
// format: .docx, .4s, .pdf, or a .zip holding any number of those (images and
// everything else in an archive are ignored). A zip member that fails to parse
// is skipped; an error means nothing at all could be read.
func Read(name string, data []byte, opt Options) (*Credits, error) {
	switch strings.ToLower(path.Ext(name)) {
	case ".zip":
		return readZip(data, opt)
	case ".docx", ".4s", ".pdf":
		f, err := readOne(name, data, opt)
		if err != nil {
			return nil, err
		}
		return merge([]file{f}), nil
	}
	return nil, fmt.Errorf("%w: %s", chgkimport.ErrUnsupported, path.Ext(name))
}

// file is one parsed packet file, numbered from its own first question,
// before merging.
type file struct {
	name       string
	format     string // docx, 4s or pdf
	questions  int
	tours      []Tour
	paragraphs []Paragraph
	authors    []Author
}

func readOne(name string, data []byte, opt Options) (file, error) {
	format := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	var source string
	if format == "4s" {
		res, err := chgkimport.Parse(name, data, "chgk")
		if err != nil {
			return file{}, err
		}
		source = res.Source
		f := dropLeadingWarmup(fromDoc(fsource.Parse(source, "chgk")), opt.Questions)
		f.name, f.format = name, format
		return f, nil
	}
	var text string
	if format == "pdf" {
		t, err := pdfText(data, opt.PDFToText)
		if err != nil {
			return file{}, err
		}
		text = unwrap(t)
	} else {
		t, err := chgkimport.DocxText(data)
		if err != nil {
			return file{}, err
		}
		text = t
	}
	// Field markers differ by language («Вопрос» / «Запитання» / «Пытанне»):
	// parse in the language the packet is written in.
	lang := language(text)
	if format == "pdf" {
		source = chgkimport.ParseTextIn(text, lang)
	} else {
		res, err := chgkimport.ParseDocxIn(name, data, lang)
		if err != nil {
			return file{}, err
		}
		source = res.Source
	}
	f := dropLeadingWarmup(fromDoc(fsource.Parse(source, "chgk")), opt.Questions)
	f.name, f.format = name, format
	return f, nil
}

// detector is built once: a model over the languages chgksuite reads.
var detector = lingua.NewLanguageDetectorBuilder().
	FromLanguages(lingua.Russian, lingua.Ukrainian, lingua.Belarusian, lingua.Kazakh, lingua.English, lingua.Serbian, lingua.Azerbaijani).
	Build()

// language names a packet's language as chgksuite's --language code ("" is
// Russian), by lingua's n-gram model over the first 20 000 bytes of text. Too
// little text to tell (an image-only file) reads as Russian, as does Uzbek,
// which lingua does not know.
func language(text string) string {
	if len(text) > 20000 {
		text = text[:20000]
	}
	if utf8.RuneCountInString(strings.TrimSpace(text)) < 200 {
		return ""
	}
	l, ok := detector.DetectLanguageOf(text)
	if !ok {
		return ""
	}
	switch l {
	case lingua.Ukrainian:
		return "ua"
	case lingua.Belarusian:
		return "by"
	case lingua.Kazakh:
		return "kz_cyr"
	case lingua.English:
		return "en"
	case lingua.Serbian:
		return "sr"
	case lingua.Azerbaijani:
		return "az"
	}
	return ""
}

func readZip(data []byte, opt Options) (*Credits, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("read zip: %w", err)
	}
	budget := int64(maxInflated)
	var files []file
	for _, zf := range zr.File {
		base := path.Base(zf.Name)
		ext := strings.ToLower(path.Ext(base))
		if zf.FileInfo().IsDir() || strings.HasPrefix(base, ".") || strings.HasPrefix(base, "~$") ||
			strings.HasPrefix(zf.Name, "__MACOSX/") || (ext != ".docx" && ext != ".4s" && ext != ".pdf") {
			continue
		}
		body, err := readEntry(zf, &budget)
		if err != nil {
			return nil, err
		}
		f, err := readOne(base, body, opt)
		if err != nil || f.questions == 0 {
			// Handouts, an answer sheet, a scan: nothing that is a packet.
			continue
		}
		f.name = zf.Name
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no packet in the archive: no .docx, .4s or .pdf with questions")
	}
	return merge(pick(files, opt.Questions)), nil
}

// formats is the order formats are trusted in: a docx is the organizer's own
// file, a 4s is already structured, a PDF is text recovered from a layout.
var formats = []string{"docx", "4s", "pdf"}

// pick chooses which of an archive's files are the packet. With the question
// count known, format by format: one file holding exactly that many questions,
// else all files of the format if together they hold it (one file per tour).
// Otherwise the most trusted format present: its biggest file alone when it has
// at least twice the questions of the next (the packet beside spare questions),
// else all of them.
func pick(files []file, questions int) []file {
	groups := map[string][]file{}
	for _, f := range dedupe(files) {
		groups[f.format] = append(groups[f.format], f)
	}
	for _, g := range groups {
		sort.SliceStable(g, func(i, j int) bool { return naturalLess(g[i].name, g[j].name) })
	}
	if questions > 0 {
		for _, format := range formats {
			sum := 0
			for _, f := range groups[format] {
				if f.questions == questions {
					return []file{f}
				}
				sum += f.questions
			}
			if sum == questions {
				return groups[format]
			}
		}
	}
	for _, format := range formats {
		g := groups[format]
		if len(g) == 0 {
			continue
		}
		biggest := slices.Clone(g)
		sort.SliceStable(biggest, func(i, j int) bool { return biggest[i].questions > biggest[j].questions })
		if len(biggest) == 1 || biggest[0].questions >= 2*biggest[1].questions {
			return biggest[:1]
		}
		return g
	}
	return files
}

// dedupe drops a file that repeats another of its format — the same packet
// twice in one archive («пакет.docx» and «пакет для ведущих.docx»): same
// question count and same tour headings. The copy with more credits
// paragraphs stays.
func dedupe(files []file) []file {
	type sig struct {
		format string
		key    string
	}
	best := map[sig]int{}
	var out []file
	for _, f := range files {
		labels := make([]string, len(f.tours))
		for i, t := range f.tours {
			labels[i] = t.Label
		}
		k := sig{f.format, fmt.Sprint(f.questions, labels)}
		if i, ok := best[k]; ok {
			if len(f.paragraphs) > len(out[i].paragraphs) {
				out[i] = f
			}
			continue
		}
		best[k] = len(out)
		out = append(out, f)
	}
	return out
}

// pdfText runs pdftotext over a PDF: reading order, no layout.
func pdfText(data []byte, bin string) (string, error) {
	if bin == "" {
		bin = "pdftotext"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return "", fmt.Errorf("pdftotext not available: %w", err)
	}
	tmp, err := os.CreateTemp("", "credits-*.pdf")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-enc", "UTF-8", "-nopgbrk", tmp.Name(), "-").Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	return string(out), nil
}

// reLabelStart is a line that opens a field or a block, and so never continues
// the line above it.
var reLabelStart = regexp.MustCompile(`^(?i:вопрос|ответ|зач[её]т|незач[её]т|комментари|источник|автор|редактор|тур(\s|$)|раздат|\d+[.)])`)

// unwrap joins the lines a PDF broke a paragraph into, which the text parser
// would otherwise read as separate paragraphs. A line continues the one above
// when that one is long (a wrapped line runs to the margin; headings are short),
// ends without closing punctuation, and this one does not open a field.
func unwrap(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if n := len(out); n > 0 && line != "" && out[n-1] != "" {
			prev := out[n-1]
			last, _ := utf8.DecodeLastRuneInString(prev)
			if utf8.RuneCountInString(prev) >= 40 && !strings.ContainsRune(".!?:;»)\"…", last) &&
				!reLabelStart.MatchString(strings.TrimSpace(line)) {
				out[n-1] = prev + " " + strings.TrimSpace(line)
				continue
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func readEntry(f *zip.File, budget *int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", f.Name, err)
	}
	defer rc.Close()
	body, err := io.ReadAll(io.LimitReader(rc, *budget+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.Name, err)
	}
	if int64(len(body)) > *budget {
		return nil, fmt.Errorf("archive too large")
	}
	*budget -= int64(len(body))
	return body, nil
}

// reWarmup is a tour heading of warm-up questions, which the tournament does
// not count.
var reWarmup = regexp.MustCompile(`^(?i:разминк)`)

// fromDoc walks one parsed file: tour headings, «meta»/«editor» paragraphs and
// the author of each question. Nothing else of a question is read.
func fromDoc(doc fsource.Doc) file {
	f := file{}
	tour := -1
	warmup := false
	var pending []int // paragraphs waiting for the next question's number
	for _, p := range doc {
		switch p.Type {
		case "section":
			label := text(p.Content)
			// A heading with no question under it before any real tour (a
			// contents line in the narrator's instructions) is not a tour: its
			// paragraphs belong to the preamble.
			if tour >= 0 && f.tours[tour].First == 0 && !f.tours[tour].Warmup && f.questions == 0 {
				for _, i := range pending {
					f.paragraphs[i].Tour = -1
				}
				f.tours = f.tours[:tour]
			}
			warmup = reWarmup.MatchString(label)
			f.tours = append(f.tours, Tour{Label: label, Warmup: warmup})
			tour = len(f.tours) - 1
			// A paragraph left at the end of a tour has no question after it;
			// the preamble's wait for the packet's first question.
			pending = slices.DeleteFunc(pending, func(i int) bool { return f.paragraphs[i].Tour >= 0 })
		case "meta", "editor":
			if s := text(p.Content); s != "" {
				f.paragraphs = append(f.paragraphs, Paragraph{Text: s, Tour: tour, Editor: p.Type == "editor"})
				pending = append(pending, len(f.paragraphs)-1)
			}
		case "Question":
			if warmup {
				continue
			}
			f.questions++
			n := f.questions
			for _, i := range pending {
				f.paragraphs[i].Next = n
			}
			pending = nil
			if tour >= 0 {
				if f.tours[tour].First == 0 {
					f.tours[tour].First = n
				}
				f.tours[tour].Last = n
			}
			if q, ok := p.Content.(*fsource.Question); ok {
				if s := text(q.Get("author")); s != "" {
					f.authors = append(f.authors, Author{Question: n, Text: s})
				}
			}
		}
	}
	return f
}

// dropLeadingWarmup un-numbers the questions that stand before the first tour
// heading of a packet that has tours: a warm-up question the packet did not
// put under «Разминка» (the preamble says «в пакете есть разминочный
// вопрос»). Numbering them would shift every tour by one against the
// tournament. When the count as parsed already equals the tournament's, the
// questions stay numbered — the packet simply has no heading for its first
// tour.
func dropLeadingWarmup(f file, want int) file {
	pre := 0
	for _, t := range f.tours {
		if t.First > 0 {
			pre = t.First - 1
			break
		}
	}
	if pre == 0 || len(f.tours) == 0 || (want > 0 && f.questions == want) {
		return f
	}
	shift := func(n int) int {
		if n == 0 {
			return 0
		}
		return max(1, n-pre)
	}
	for i := range f.tours {
		f.tours[i].First, f.tours[i].Last = shift(f.tours[i].First), shift(f.tours[i].Last)
	}
	for i := range f.paragraphs {
		f.paragraphs[i].Next = shift(f.paragraphs[i].Next)
	}
	authors := f.authors[:0]
	for _, a := range f.authors {
		if a.Question > pre {
			a.Question -= pre
			authors = append(authors, a)
		}
	}
	f.authors = authors
	f.questions -= pre
	return f
}

// merge puts the files of a packet in order and joins them, renumbering the
// questions through. Files are ordered by the number of their first tour
// («Тур 3»), falling back to a natural sort of the file names, so a zip of one
// docx per tour comes out in tour order. A later file's preamble stands in that
// file's first tour (a per-tour docx often puts «Редактор — …» above its
// «Тур N»); a later file with no tour heading becomes a tour named after it.
func merge(files []file) *Credits {
	sort.SliceStable(files, func(i, j int) bool {
		ni, oki := firstTourNumber(files[i])
		nj, okj := firstTourNumber(files[j])
		if oki && okj && ni != nj {
			return ni < nj
		}
		return naturalLess(files[i].name, files[j].name)
	})
	out := &Credits{Tours: []Tour{}, Paragraphs: []Paragraph{}, Authors: []Author{}, Sources: []string{}}
	shift := func(n, by int) int {
		if n == 0 {
			return 0
		}
		return n + by
	}
	for i, f := range files {
		out.Sources = append(out.Sources, f.name)
		offset, base := out.Questions, len(out.Tours)
		if i > 0 && len(f.tours) == 0 && f.questions > 0 {
			f.tours = []Tour{{Label: strings.TrimSuffix(path.Base(f.name), path.Ext(f.name)), First: 1, Last: f.questions}}
			for k := range f.paragraphs {
				f.paragraphs[k].Tour = 0
			}
		}
		for _, t := range f.tours {
			t.Label = truncate(t.Label, maxLabel)
			t.First, t.Last = shift(t.First, offset), shift(t.Last, offset)
			out.Tours = append(out.Tours, t)
		}
		for _, p := range f.paragraphs {
			if utf8.RuneCountInString(p.Text) > maxParagraph {
				continue // not a credit — dropped, not cut
			}
			switch {
			case p.Tour >= 0:
				p.Tour += base
			case i > 0 && len(f.tours) > 0:
				p.Tour = base
			}
			p.Next = shift(p.Next, offset)
			out.Paragraphs = append(out.Paragraphs, p)
		}
		for _, a := range f.authors {
			a.Question += offset
			a.Text = truncate(a.Text, maxAuthor)
			out.Authors = append(out.Authors, a)
		}
		out.Questions += f.questions
	}
	if len(out.Tours) > maxTours {
		out.Tours = out.Tours[:maxTours]
	}
	if len(out.Paragraphs) > maxParagraphs {
		out.Paragraphs = out.Paragraphs[:maxParagraphs]
	}
	if len(out.Authors) > maxAuthors {
		out.Authors = out.Authors[:maxAuthors]
	}
	return out
}

var reNumber = regexp.MustCompile(`\d+`)

func firstTourNumber(f file) (int, bool) {
	if len(f.tours) == 0 {
		return 0, false
	}
	m := reNumber.FindString(f.tours[0].Label)
	if m == "" {
		return 0, false
	}
	n, err := strconv.Atoi(m)
	return n, err == nil
}

// The input is an untrusted upload: a crafted file must not be able to make
// the output large. Real packets have a few dozen credits paragraphs.
const (
	maxTours      = 100
	maxParagraphs = 500
	maxParagraph  = 3000 // runes
	maxAuthors    = 1000
	maxAuthor     = 500 // runes
	maxLabel      = 200 // runes
)

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// naturalLess compares names with digit runs taken as numbers: «тур 2» < «тур 10».
func naturalLess(a, b string) bool {
	ar, br := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	for len(ar) > 0 && len(br) > 0 {
		if unicode.IsDigit(ar[0]) && unicode.IsDigit(br[0]) {
			da, db := digits(ar), digits(br)
			na, _ := strconv.Atoi(string(ar[:da]))
			nb, _ := strconv.Atoi(string(br[:db]))
			if na != nb {
				return na < nb
			}
			ar, br = ar[da:], br[db:]
			continue
		}
		if ar[0] != br[0] {
			return ar[0] < br[0]
		}
		ar, br = ar[1:], br[1:]
	}
	return len(ar) < len(br)
}

func digits(r []rune) int {
	n := 0
	for n < len(r) && unicode.IsDigit(r[n]) {
		n++
	}
	return n
}

// text flattens an element's content: a string as is, a list (fsource's
// process_list shapes) one item per line with its dash back.
func text(v any) string {
	switch c := v.(type) {
	case string:
		return strings.TrimSpace(c)
	case []any:
		var lines []string
		for _, item := range c {
			switch it := item.(type) {
			case string:
				lines = append(lines, "- "+it)
			case []any:
				for _, sub := range it {
					if s, ok := sub.(string); ok {
						lines = append(lines, "- "+s)
					}
				}
			}
		}
		// The [preamble, [items…]] form: the preamble is not an item.
		if len(c) == 2 {
			if pre, ok := c[0].(string); ok {
				if _, ok := c[1].([]any); ok {
					lines[0] = pre
				}
			}
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return ""
}
