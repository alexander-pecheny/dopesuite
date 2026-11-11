package credits

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/chgkimport"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/fsource"
)

// ErrNoQuestion is a packet with no question under the number asked for, or
// with more than one (tours numbered from 1 each): nothing is guessed.
var ErrNoQuestion = errors.New("no such question")

// QuestionText is one question of a packet: its number as the packet writes it
// and the question in 4s (every field, the number left out), for a renderer of
// 4s to draw. File is the document it was read from, Language that document's
// language as chgksuite's --language code, "ru" for Russian.
type QuestionText struct {
	Number   string `json:"number"`
	Source   string `json:"source"`
	Language string `json:"language"`
	File     string `json:"file"`
}

// Question reads the question numbered number out of an uploaded file, read as
// Read reads it: the same formats, and in an archive the same files picked as
// the packet. The number must equal the packet's own, as parsed; questions are
// never counted.
func Question(name string, data []byte, number string, opt Options) (*QuestionText, error) {
	files, err := packetFiles(name, data, opt)
	if err != nil {
		return nil, err
	}
	number = strings.TrimSpace(number)
	var found []QuestionText
	for _, f := range files {
		for _, p := range f.doc {
			q, ok := p.Content.(*fsource.Question)
			if p.Type != "Question" || !ok || !q.Has("number") || strings.TrimSpace(fmt.Sprint(q.Get("number"))) != number {
				continue
			}
			language := f.language
			if language == "" {
				language = "ru"
			}
			found = append(found, QuestionText{
				Number:   number,
				Source:   strings.TrimSpace(fsource.Compose(fsource.Doc{p}, fsource.NumbersNone)),
				Language: language,
				File:     f.name,
			})
		}
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("%w: %d questions numbered %q", ErrNoQuestion, len(found), number)
	}
	return &found[0], nil
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
