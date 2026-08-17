package schemedsl

import (
	"strings"
	"testing"
)

const exampleSrc = `
[defaults]
venues: [Москва-1, Москва-2, Рим]
sorting: [points, h2h, taken, diff]
points: [2, 1, 0]
questions: 5

[init]
seed: kvrm  # or: random, xlsx
sorting: [points desc, rating desc]

[scheme]
kind: roundrobin
groups: 12
group_size: 4
proceeding_participants: 2
---
kind: double_elimination
groups: 6
group_size: 4
proceeding_participants: 2
---
kind: roundrobin
groups: 4
group_size: 3
reseed: true
proceeding_participants: 2
questions: 7
questions.r3: 9
`

func TestParseExample(t *testing.T) {
	doc, err := Parse(exampleSrc)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := doc.Defaults.Int("questions"); got != 5 {
		t.Fatalf("defaults questions = %d", got)
	}
	venues, ok, err := doc.Defaults.List("venues")
	if err != nil || !ok || len(venues) != 3 || venues[2] != "Рим" {
		t.Fatalf("venues = %v ok=%v err=%v", venues, ok, err)
	}
	if got, _ := doc.Init.Str("seed"); got != "kvrm" {
		t.Fatalf("seed = %q (comment not stripped?)", got)
	}
	sorting, ok, err := doc.Init.Sorting("sorting")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if sorting[0].Metric != "points" || sorting[0].Dir != "desc" || sorting[1].Metric != "rating" {
		t.Fatalf("init sorting = %+v", sorting)
	}
	if len(doc.Blocks) != 3 {
		t.Fatalf("blocks = %d", len(doc.Blocks))
	}
	if got, _ := doc.Blocks[1].Str("kind"); got != "double_elimination" {
		t.Fatalf("block 2 type = %q", got)
	}
	if got, ok := doc.Blocks[2].Bool("reseed"); !ok || !got {
		t.Fatalf("block 3 reseed = %v ok=%v", got, ok)
	}
	if got, _ := doc.Blocks[2].Int("questions.r3"); got != 9 {
		t.Fatalf("dotted questions.r3 = %d", got)
	}
}

// The parser reports the direction the scheme wrote, and nothing more: an
// unqualified metric leaves it empty so the compiler can read it off the metric
// itself. Deciding here would override место and жребий, which are lower-better.
func TestParseSortingDirections(t *testing.T) {
	doc, err := Parse("[defaults]\nsorting: [points, place_sum, taken desc]\n")
	if err != nil {
		t.Fatal(err)
	}
	sorting, _, err := doc.Defaults.Sorting("sorting")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"", "", "desc"} {
		if sorting[i].Dir != want {
			t.Errorf("%s dir = %q, want %q", sorting[i].Metric, sorting[i].Dir, want)
		}
	}
	for i, want := range []string{"desc", "asc", "desc"} {
		if got := sortDir(sorting[i]); got != want {
			t.Errorf("%s: компилятор выбрал %q, want %q", sorting[i].Metric, got, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name, src, wantSubstr string
		wantLine              int
	}{
		{"unknown section", "[whatever]\n", "[whatever]", 1},
		{"key before section", "kind: rr\n", "секции", 1},
		{"separator outside scheme", "[defaults]\nquestions: 5\n---\n", "---", 3},
		{"duplicate key", "[defaults]\nquestions: 5\nquestions: 7\n", "questions", 3},
		{"free text", "[defaults]\nlorem ipsum\n", "lorem", 2},
		{"unclosed list", "[defaults]\nsorting: [points\n", "sorting", 2},
		{"int wanted", "[defaults]\nquestions: five\n", "questions", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse(tc.src)
			if err == nil {
				switch tc.name {
				case "int wanted":
					_, err = doc.Defaults.IntErr("questions")
				case "unclosed list":
					_, _, err = doc.Defaults.List("sorting")
				}
			}
			if err == nil {
				t.Fatal("no error")
			}
			perr, ok := err.(*Error)
			if !ok {
				t.Fatalf("not a *Error: %v", err)
			}
			if perr.Line != tc.wantLine {
				t.Fatalf("line = %d, want %d (%v)", perr.Line, tc.wantLine, err)
			}
			if !strings.Contains(perr.Error(), tc.wantSubstr) {
				t.Fatalf("error %q lacks %q", perr.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestParseEmptyBlocksDropped(t *testing.T) {
	doc, err := Parse("[scheme]\nkind: roundrobin\ngroup_size: 4\n---\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 1 {
		t.Fatalf("trailing --- should not create an empty block: %d", len(doc.Blocks))
	}
}
