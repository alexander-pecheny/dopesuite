// qy-langdetect tells which language each of a batch of questions is written
// in: Russian, Ukrainian or Belarusian. It reads JSON lines {"id", "text"} on
// stdin and writes {"id", "lang"} for each on stdout. qy-server runs it from
// its background pass (platform/langdetect) rather than linking it: lingua-go
// embeds the models of all 75 of its languages, some 130 MB, whichever ones a
// detector uses.
//
// lingua was chosen over whatlanggo, which weighs nothing, on 163
// hand-labelled gotquestions questions (60 ru, 52 uk, 51 be), the contest's
// own 223 and a few short ones: lingua missed one of the 163 (a Russian
// question that quotes Ukrainian verse), whatlanggo also misread short texts
// both ways. The input is the text and the answer: the comment quotes and
// translates freely, and reading it cost every detector three to seven
// questions. An admin corrects a miss.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/pemistahl/lingua-go"

	"qy/qy/domain/lang"
)

type item struct {
	ID   int64  `json:"id"`
	Text string `json:"text,omitempty"`
	Lang string `json:"lang,omitempty"`
}

var codes = map[lingua.Language]string{
	lingua.Russian: lang.Russian, lingua.Ukrainian: lang.Ukrainian, lingua.Belarusian: lang.Belarusian,
}

func newDetector() lingua.LanguageDetector {
	return lingua.NewLanguageDetectorBuilder().FromLanguages(lingua.Russian, lingua.Ukrainian, lingua.Belarusian).Build()
}

// detect names text's language; one it cannot place, an empty text say, is
// Russian.
func detect(d lingua.LanguageDetector, text string) string {
	if l, ok := d.DetectLanguageOf(text); ok {
		if c, ok := codes[l]; ok {
			return c
		}
	}
	return lang.Russian
}

// maxLine is the longest line a question makes, generous: questions run to a
// few kilobytes.
const maxLine = 4 * mebibyte

const mebibyte = 1 << 20

func run(in io.Reader, out io.Writer) error {
	d := newDetector()
	sc := bufio.NewScanner(in)
	sc.Buffer(nil, maxLine)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		var it item
		if err := json.Unmarshal(sc.Bytes(), &it); err != nil {
			return err
		}
		if err := enc.Encode(item{ID: it.ID, Lang: detect(d, it.Text)}); err != nil {
			return err
		}
	}
	return sc.Err()
}

func main() {
	w := bufio.NewWriter(os.Stdout)
	err := run(os.Stdin, w)
	if ferr := w.Flush(); err == nil {
		err = ferr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "qy-langdetect:", err)
		os.Exit(1)
	}
}
