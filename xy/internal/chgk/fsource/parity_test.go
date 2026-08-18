package fsource_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/handout"
	"xy/internal/chgk/inline"
)

// testdata/parity.json is the corpus the TypeScript 4s reader is checked against
// (jstest/fsource_parity.test.js): every line of every fixture split by
// SplitMarker, the (img …) references of a few sources, and one .hndt document
// the browser generates and this side parses. Go is the oracle for what it
// computes; -update rewrites those parts of the file.
var update = flag.Bool("update", false, "rewrite testdata/parity.json from this side's output")

type parityLine struct {
	Line   string `json:"line"`
	Prefix string `json:"prefix"`
	Rest   string `json:"rest"`
}

type parityImg struct {
	Source string   `json:"source"`
	Refs   []string `json:"refs"`
}

type parityCorpus struct {
	Note       string       `json:"_"`
	ExtraLines []string     `json:"extraLines"`
	Lines      []parityLine `json:"lines"`
	ImgRefs    []parityImg  `json:"imgRefs"`
	Hndt       struct {
		Cards   json.RawMessage  `json:"cards"`
		Numbers json.RawMessage  `json:"numbers"`
		Metas   json.RawMessage  `json:"metas"`
		Text    string           `json:"text"`
		Blocks  []map[string]any `json:"blocks"`
	} `json:"hndt"`
}

const corpusPath = "testdata/parity.json"

func TestParity(t *testing.T) {
	raw, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	var c parityCorpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("corpus json: %v", err)
	}

	// Lines: every line of every fixture, plus the hand-picked extras.
	fixtures, _ := filepath.Glob("testdata/*.4s")
	sort.Strings(fixtures)
	var inputs []string
	for _, f := range fixtures {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, strings.Split(strings.TrimRight(string(src), "\n"), "\n")...)
	}
	inputs = append(inputs, c.ExtraLines...)
	var lines []parityLine
	seen := map[string]bool{}
	for _, l := range inputs {
		if seen[l] {
			continue
		}
		seen[l] = true
		p, r := fsource.SplitMarker(l)
		lines = append(lines, parityLine{Line: l, Prefix: p, Rest: r})
	}

	imgs := make([]parityImg, len(c.ImgRefs))
	for i, x := range c.ImgRefs {
		imgs[i] = parityImg{Source: x.Source, Refs: imgRefs(x.Source)}
	}

	// Blocks are typed by the parser: ints, floats and strings. Round-trip them
	// through JSON so the comparison sees what the file holds (float64s).
	blocksJSON, _ := json.Marshal(handout.Blocks(c.Hndt.Text))
	var blocks []map[string]any
	_ = json.Unmarshal(blocksJSON, &blocks)

	if *update {
		c.Lines, c.ImgRefs, c.Hndt.Blocks = lines, imgs, blocks
		out, _ := json.MarshalIndent(c, "", "  ")
		if err := os.WriteFile(corpusPath, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !reflect.DeepEqual(c.Lines, lines) {
		t.Errorf("parity.json lines are stale: run go test ./internal/chgk/fsource -run TestParity -update")
	}
	if !reflect.DeepEqual(c.ImgRefs, imgs) {
		t.Errorf("parity.json imgRefs are stale (got %v): run -update", imgs)
	}
	if !reflect.DeepEqual(c.Hndt.Blocks, blocks) {
		t.Errorf("parity.json hndt.blocks are stale (got %v): run -update", blocks)
	}
	// What the browser generated must parse into one block per handout, each
	// naming its question — the .hndt round trip.
	if len(blocks) != 3 {
		t.Fatalf("hndt: want 3 blocks, got %d", len(blocks))
	}
	for i, want := range []string{"1", "3", "4"} {
		if blocks[i]["for_question"] != want {
			t.Errorf("hndt block %d: for_question = %v, want %v", i, blocks[i]["for_question"], want)
		}
	}
	if blocks[0]["image"] != "pic.png" || blocks[1]["text"] == nil {
		t.Errorf("hndt: block 0 image = %v, block 1 text = %v", blocks[0]["image"], blocks[1]["text"])
	}
}

// imgRefs is the Go spelling of chgk.ts's imgRefs: the (img …) directives the
// inline tokenizer finds, hidden comments dropped, first-appearance order.
func imgRefs(source string) []string {
	out := []string{}
	for _, r := range inline.Parse4sElem(source) {
		if r.Kind != "img" {
			continue
		}
		im, ok := inline.ParseImg(r.Text)
		if !ok {
			continue
		}
		dup := false
		for _, n := range out {
			if n == im.Name {
				dup = true
			}
		}
		if !dup {
			out = append(out, im.Name)
		}
	}
	return out
}
