package textparse

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"xy/internal/chgk/corpus"
	"xy/internal/chgk/fsource"
)

// The generated corpus renders every chgk canon back into the plain text of a
// package, in each way packages are commonly written, and parses that text.

// questionStyle is how a package marks where a question starts.
type questionStyle int

const (
	labelled questionStyle = iota // "Вопрос 1. …"
	numbered                      // "1. …"
	bare                          // nothing: the question is whatever precedes its answer
)

var styleNames = []string{"labelled", "numbered", "bare"}

// fieldOrders are the orders the answer-side fields are written in.
var fieldOrders = []struct {
	name   string
	fields []string
}{
	{"usual", []string{"answer", "zachet", "nezachet", "comment", "source", "author"}},
	{"comment_first", []string{"comment", "answer", "zachet", "nezachet", "source", "author"}},
	{"source_before_comment", []string{"answer", "zachet", "nezachet", "source", "comment", "author"}},
	{"author_before_source", []string{"answer", "zachet", "nezachet", "comment", "author", "source"}},
	{"zachet_after_comment", []string{"answer", "comment", "zachet", "nezachet", "source", "author"}},
}

// layouts are the two ways paragraphs reach the parser: a .docx gives a blank
// line between any two paragraphs, a .txt only between questions.
var layouts = []struct {
	name     string
	paraSep  string
	blockSep string
}{
	{"docx", "\n\n", "\n\n"},
	{"txt", "\n", "\n\n"},
}

var fieldLabels = map[string]string{
	"handout": "Раздаточный материал: ", "answer": "Ответ: ", "zachet": "Зачёт: ",
	"nezachet": "Незачёт: ", "comment": "Комментарий: ", "source": "Источник: ",
	"author": "Автор: ",
}

// generatedCase is one rendering of one canon, and the canon as a parse
// composes it.
type generatedCase struct {
	name, text, canon string
}

func generatedCases(t *testing.T) []generatedCase {
	var cases []generatedCase
	for _, file := range corpus.Chgk(t) {
		doc := fsource.Parse(corpus.Canon(t, file), "chgk")
		canon := fsource.Compose(doc, fsource.NumbersDefault)
		for style := range styleNames {
			for _, order := range fieldOrders {
				for _, layout := range layouts {
					name := strings.Join([]string{corpus.Name(file), styleNames[style], order.name, layout.name}, "/")
					r := renderer{style: questionStyle(style), order: order.fields, paraSep: layout.paraSep}
					cases = append(cases, generatedCase{name, r.render(doc, layout.blockSep), canon})
				}
			}
		}
	}
	return cases
}

type renderer struct {
	style   questionStyle
	order   []string
	paraSep string
	counter int
}

func (r *renderer) render(doc fsource.Doc, blockSep string) string {
	var blocks []string
	for _, p := range doc {
		if p.Type != "Question" {
			blocks = append(blocks, valueLines(p.Content, false)...)
			continue
		}
		blocks = append(blocks, r.question(p.Content.(*fsource.Question)))
	}
	return strings.Join(blocks, blockSep) + "\n"
}

func (r *renderer) question(q *fsource.Question) string {
	r.counter++
	if n, ok := q.Get("number").(int); ok {
		r.counter = n
	} else if s, ok := q.Get("number").(string); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			r.counter = n
		}
	}
	var paras []string
	if q.Has("handout") {
		paras = append(paras, prefixFirst(fieldLabels["handout"], valueLines(q.Get("handout"), false))...)
	}
	paras = append(paras, valueLines(q.Get("question"), true)...)
	switch r.style {
	case labelled:
		paras = prefixFirst(fmt.Sprintf("Вопрос %d. ", r.counter), paras)
	case numbered:
		paras = prefixFirst(fmt.Sprintf("%d. ", r.counter), paras)
	}
	for _, f := range r.order {
		if q.Has(f) {
			paras = append(paras, prefixFirst(fieldLabels[f], valueLines(q.Get(f), true))...)
		}
	}
	return strings.Join(paras, r.paraSep)
}

// valueLines spells a 4s value as the paragraphs a package would hold: a list
// becomes numbered items when numbered is set, plain lines otherwise.
func valueLines(v any, numberedLists bool) []string {
	switch t := v.(type) {
	case string:
		return strings.Split(t, "\n")
	case int:
		return []string{strconv.Itoa(t)}
	case []any:
		if len(t) == 2 {
			if items, ok := t[1].([]any); ok {
				return append(valueLines(t[0], numberedLists), listLines(items, numberedLists)...)
			}
		}
		return listLines(t, numberedLists)
	}
	return []string{fmt.Sprint(v)}
}

func listLines(items []any, numberedLists bool) []string {
	var out []string
	for i, x := range items {
		lines := valueLines(x, numberedLists)
		if numberedLists && len(lines) > 0 {
			lines[0] = strconv.Itoa(i+1) + ". " + lines[0]
		}
		out = append(out, lines...)
	}
	return out
}

func prefixFirst(prefix string, lines []string) []string {
	if len(lines) == 0 {
		return []string{strings.TrimSpace(prefix)}
	}
	lines[0] = prefix + lines[0]
	return lines
}

var update = flag.Bool("update", false, "rewrite testdata/generated_canon.txt")

const generatedCanonFile = "testdata/generated_canon.txt"

// TestGeneratedReadsBack is a ratchet: every generated package listed in
// generated_canon.txt must still be read back into its canon. One that newly
// is fails the test too, until -update adds it to the list.
func TestGeneratedReadsBack(t *testing.T) {
	cases := generatedCases(t)
	got := make([]string, len(cases))
	var wg sync.WaitGroup
	next := make(chan int)
	for range runtime.NumCPU() {
		wg.Go(func() {
			for i := range next {
				got[i] = fsource.Compose(Parse(cases[i].text, Options{}), fsource.NumbersDefault)
			}
		})
	}
	for i := range cases {
		next <- i
	}
	close(next)
	wg.Wait()

	var readBack []string
	for i, c := range cases {
		if got[i] == c.canon {
			readBack = append(readBack, c.name)
		}
	}
	if *update {
		if err := os.WriteFile(generatedCanonFile, []byte(strings.Join(readBack, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(generatedCanonFile)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, name := range strings.Fields(string(raw)) {
		listed[name] = true
	}
	byName := map[string]int{}
	for i, c := range cases {
		byName[c.name] = i
	}
	for name := range listed {
		if i, ok := byName[name]; ok && got[i] != cases[i].canon {
			t.Errorf("%s no longer reads back:\n%s", name, firstDiff(cases[i].canon, got[i]))
		}
	}
	for _, name := range readBack {
		if !listed[name] {
			t.Errorf("%s now reads back; run go test -run TestGeneratedReadsBack -update", name)
		}
	}
	t.Logf("%d of %d generated packages read back into their canon", len(readBack), len(cases))
}
