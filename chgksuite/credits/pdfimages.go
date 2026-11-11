package credits

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/chgkimport"
)

// A PDF's pictures (Question). pdftotext's text stays what credits reads; the
// pictures come from pdftohtml -xml, which writes each image out with its place
// on the page, and pdftotext -bbox-layout gives the place of every text line
// pdftotext prints. A picture goes in as an (img …) line after the last line
// that ends above it on its page, or first on the page when none does.

// pdfMinImageSide drops what is no picture: a rule, a bullet, a logo's speck.
const pdfMinImageSide = 16

// pdfLineSlack is how far, in points, a line may reach below a picture's top
// and still stand above it.
const pdfLineSlack = 2

// pdfTextImages is pdfText with the pictures. Without pdftohtml, or when
// reading the pictures fails, the text comes alone.
func pdfTextImages(data []byte, bin, prefix string) (string, []chgkimport.Image, error) {
	if bin == "" {
		bin = "pdftotext"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return "", nil, fmt.Errorf("pdftotext not available: %w", err)
	}
	dir, err := os.MkdirTemp("", "credits-pdf-*")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return "", nil, err
	}
	// The pages apart (no -nopgbrk), to place the pictures page by page.
	out, err := poppler(bin, "-enc", "UTF-8", in, "-")
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) && bytes.Contains(ee.Stderr, []byte("Incorrect password")) {
		return "", nil, fmt.Errorf("%w: password-protected PDF", ErrEncrypted)
	}
	if err != nil {
		return "", nil, fmt.Errorf("pdftotext: %w", err)
	}
	pages := strings.Split(string(out), "\f")
	text := strings.Join(pages, "")
	html := filepath.Join(filepath.Dir(bin), "pdftohtml")
	if filepath.Dir(bin) == "." {
		html = "pdftohtml"
	}
	if _, err := exec.LookPath(html); err != nil {
		return text, nil, nil
	}
	pictures, err := pdfPictures(html, in, dir)
	if err != nil || len(pictures) == 0 {
		return text, nil, nil
	}
	layout, err := poppler(bin, "-bbox-layout", "-enc", "UTF-8", in, "-")
	if err != nil {
		return text, nil, nil
	}
	lines := pdfLines(layout)
	var images []chgkimport.Image
	for i, page := range pages {
		var at []pdfInsert
		ends := pdfLineEnds(page, lines[i])
		for _, pic := range pictures[i] {
			if len(images) == maxQuestionImages {
				break
			}
			name := prefix + "image" + strconv.Itoa(len(images)+1) + pic.ext
			images = append(images, chgkimport.Image{Name: name, MIME: pictureMIME(name), Data: pic.data})
			at = append(at, pdfInsert{offset: pdfAnchor(lines[i], ends, pic.top), text: "\n(img " + name + ")\n"})
		}
		sort.SliceStable(at, func(a, b int) bool { return at[a].offset > at[b].offset })
		for _, ins := range at {
			page = page[:ins.offset] + ins.text + page[ins.offset:]
		}
		pages[i] = page
	}
	return strings.Join(pages, ""), images, nil
}

type pdfInsert struct {
	offset int
	text   string
}

// pdfLine is a line pdftotext prints: its words and where it ends on the page.
type pdfLine struct {
	text string
	yMax float64
}

// pdfPicture is an image of a page: where its top is and its file's bytes.
type pdfPicture struct {
	top  float64
	ext  string
	data []byte
}

func poppler(bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pdftotextTimeout)
	defer cancel()
	return exec.CommandContext(ctx, bin, args...).Output()
}

// pdfPictures runs pdftohtml -xml over in and gives each page's pictures in
// order down the page: PNG and JPEG files of at least pdfMinImageSide.
func pdfPictures(html, in, dir string) (map[int][]pdfPicture, error) {
	base := filepath.Join(dir, "p")
	if _, err := poppler(html, "-xml", "-zoom", "1", "-q", "-nodrm", "-fmt", "png", in, base); err != nil {
		return nil, err
	}
	f, err := os.Open(base + ".xml")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	dec.Strict = false
	out := map[int][]pdfPicture{}
	page := -1
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, nil
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch el.Name.Local {
		case "page":
			page++
		case "image":
			attr := attrs(el)
			w, _ := strconv.ParseFloat(attr["width"], 64)
			h, _ := strconv.ParseFloat(attr["height"], 64)
			top, err := strconv.ParseFloat(attr["top"], 64)
			// The file is pdftohtml's, in dir: only its name is taken.
			src := filepath.Join(dir, filepath.Base(attr["src"]))
			ext := strings.ToLower(filepath.Ext(src))
			if err != nil || page < 0 || w < pdfMinImageSide || h < pdfMinImageSide || (ext != ".png" && ext != ".jpg") {
				continue
			}
			data, err := os.ReadFile(src)
			if err != nil || len(data) == 0 || len(data) > maxQuestionImageBytes {
				continue
			}
			out[page] = append(out[page], pdfPicture{top: top, ext: ext, data: data})
		}
	}
	for p := range out {
		sort.SliceStable(out[p], func(a, b int) bool { return out[p][a].top < out[p][b].top })
	}
	return out, nil
}

// pdfLines reads pdftotext -bbox-layout: each page's lines in the order
// pdftotext prints them, words joined by a space as it joins them.
func pdfLines(layout []byte) map[int][]pdfLine {
	dec := xml.NewDecoder(bytes.NewReader(layout))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	out := map[int][]pdfLine{}
	page := -1
	var words []string
	var yMax float64
	inWord := false
	var word strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "page":
				page++
			case "line":
				words = words[:0]
				yMax, _ = strconv.ParseFloat(attrs(t)["yMax"], 64)
			case "word":
				inWord = true
				word.Reset()
			}
		case xml.CharData:
			if inWord {
				word.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "word":
				inWord = false
				words = append(words, word.String())
			case "line":
				if page >= 0 {
					out[page] = append(out[page], pdfLine{text: strings.Join(words, " "), yMax: yMax})
				}
			}
		}
	}
}

// pdfLineEnds finds each line in the page's text, in order: where it ends, or
// -1 for a line the text does not hold as written.
func pdfLineEnds(page string, lines []pdfLine) []int {
	ends := make([]int, len(lines))
	from := 0
	for i, l := range lines {
		ends[i] = -1
		if l.text == "" {
			continue
		}
		if at := strings.Index(page[from:], l.text); at >= 0 {
			from += at + len(l.text)
			ends[i] = from
		}
	}
	return ends
}

// pdfAnchor is where a picture whose top is at top goes in the page's text:
// after the lowest line ending above it, or at the page's start.
func pdfAnchor(lines []pdfLine, ends []int, top float64) int {
	best, bestY := 0, -1.0
	for i, l := range lines {
		if ends[i] >= 0 && l.yMax <= top+pdfLineSlack && l.yMax >= bestY {
			best, bestY = ends[i], l.yMax
		}
	}
	return best
}

func attrs(el xml.StartElement) map[string]string {
	m := map[string]string{}
	for _, a := range el.Attr {
		m[a.Name.Local] = a.Value
	}
	return m
}
