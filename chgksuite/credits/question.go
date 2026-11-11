package credits

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/chgkimport"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/fsource"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/listexport"
)

// ErrNoQuestion is a packet with no question under the number asked for, or
// with more than one (tours numbered from 1 each): nothing is guessed.
var ErrNoQuestion = errors.New("no such question")

// QuestionText is one question of a packet: its number as the packet writes it
// and the question in 4s (every field, the number left out), for a renderer of
// 4s to draw. File is the document it was read from, Language that document's
// language as chgksuite's --language code, "ru" for Russian. Images are the
// pictures its (img …) directives name that the file or its archive holds:
// a .docx's, a .doc's (docread), an .rtf's (rtfread), a PDF's (placed by
// pdftohtml, pdfimages.go), those beside a .4s in an archive; raster ones only,
// within the caps below. A picture left out is drawn as missing.
type QuestionText struct {
	Number   string          `json:"number"`
	Source   string          `json:"source"`
	Language string          `json:"language"`
	File     string          `json:"file"`
	Images   []QuestionImage `json:"images"`
}

// QuestionImage is one picture, named as the (img …) directive names it. Data
// goes into JSON as base64.
type QuestionImage struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Data []byte `json:"data"`
}

// The caps on a question's pictures: a page shows them, a site stores them.
const (
	maxQuestionImages     = 20
	maxQuestionImageBytes = 5 << 20
	maxQuestionImagesSize = 15 << 20
)

// pictureMIME is the type of a raster picture a browser draws by its name, ""
// for anything else: an SVG may carry script, a TIFF draws nowhere.
func pictureMIME(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	}
	return ""
}

// questionImages are the pictures source names, out of the file's.
func questionImages(source string, f file) []QuestionImage {
	byName := map[string]chgkimport.Image{}
	for _, img := range f.images {
		if _, ok := byName[img.Name]; !ok {
			byName[img.Name] = img
		}
	}
	out := []QuestionImage{}
	total := 0
	for _, name := range listexport.ImageRefs(source) {
		img, ok := byName[name]
		mime := pictureMIME(name)
		if !ok || mime == "" || len(img.Data) == 0 || len(img.Data) > maxQuestionImageBytes ||
			total+len(img.Data) > maxQuestionImagesSize || len(out) == maxQuestionImages {
			continue
		}
		total += len(img.Data)
		out = append(out, QuestionImage{Name: name, MIME: mime, Data: img.Data})
	}
	return out
}

// Question reads the question numbered number out of an uploaded file, read as
// Read reads it: the same formats, and in an archive the same files picked as
// the packet. The number is chgksuite's own: the one the packet writes, as its
// text parser reads it; for a question it reads none for (a number Word's list
// numbering draws, a number on a line of its own that chgksuite's «smart» mode
// leaves alone), the one chgksuite's 4s gives it, counting on from the written
// numbers. A written number wins over a counted one: a note to the narrator
// read as an unnumbered question does not take «Вопрос 1.»'s number.
func Question(name string, data []byte, number string, opt Options) (*QuestionText, error) {
	opt.images = true
	files, err := packetFiles(name, data, opt)
	if err != nil {
		return nil, err
	}
	number = strings.TrimSpace(number)
	var written, counted []QuestionText
	for _, f := range files {
		language := f.language
		if language == "" {
			language = "ru"
		}
		for _, nq := range numberedQuestions(f.doc) {
			var into *[]QuestionText
			switch {
			case nq.written == number:
				into = &written
			case nq.written == "" && nq.counted == number:
				into = &counted
			default:
				continue
			}
			source := strings.TrimSpace(fsource.Compose(fsource.Doc{nq.pair}, fsource.NumbersNone))
			*into = append(*into, QuestionText{
				Number:   number,
				Source:   source,
				Language: language,
				File:     f.name,
				Images:   questionImages(source, f),
			})
		}
	}
	found := written
	if len(found) == 0 {
		found = counted
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("%w: %d questions numbered %q", ErrNoQuestion, len(found), number)
	}
	return &found[0], nil
}

// numberedQuestion is a question of a file with its numbers: written, the one
// the text parser read ("" for none), and counted, the one chgksuite's 4s gives
// it ("" when the 4s does not line up with the parse).
type numberedQuestion struct {
	pair             fsource.Pair
	written, counted string
}

// numberedQuestions numbers doc's questions as chgksuite does: doc written out
// as 4s with every written number (NumbersAll) and read back, so a question with
// no number of its own takes the next after the one before it.
func numberedQuestions(doc fsource.Doc) []numberedQuestion {
	var out []numberedQuestion
	for _, p := range doc {
		if q, ok := p.Content.(*fsource.Question); ok && p.Type == "Question" {
			nq := numberedQuestion{pair: p}
			if q.Has("number") {
				nq.written = strings.TrimSpace(fmt.Sprint(q.Get("number")))
			}
			out = append(out, nq)
		}
	}
	var counted []string
	for _, p := range fsource.Parse(fsource.Compose(doc, fsource.NumbersAll), "chgk") {
		if q, ok := p.Content.(*fsource.Question); ok && p.Type == "Question" && q.Has("number") {
			counted = append(counted, strings.TrimSpace(fmt.Sprint(q.Get("number"))))
		} else if ok && p.Type == "Question" {
			counted = append(counted, "")
		}
	}
	if len(counted) == len(out) {
		for i := range out {
			out[i].counted = counted[i]
		}
	}
	return out
}

// packetFiles are the files Read takes the credits from.
func packetFiles(name string, data []byte, opt Options) ([]file, error) {
	ext := strings.ToLower(path.Ext(name))
	switch {
	case slices.Contains(archives, ext):
		return archiveFiles(ext, data, opt)
	case slices.Contains(documents, ext):
		f, err := readOne(name, data, opt)
		if err != nil {
			return nil, err
		}
		return []file{f}, nil
	}
	return nil, fmt.Errorf("%w: %s", chgkimport.ErrUnsupported, path.Ext(name))
}
