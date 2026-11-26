// Package fallbackfont holds the faces typst falls back on where Noto Sans has
// no glyph: one Noto family per script Noto Sans lacks (Armenian, Georgian,
// Hebrew, Arabic, CJK, …), built by scripts/fallbackfonts.py.
//
// The documents name Noto Sans alone. typst falls back to any loaded face that
// has the glyph, so loading these is what keeps such a word from coming out as
// tofu. The typst binary gets them all in its --font-path (Files), which costs
// nothing until it reads one. The wasm pool would hold a copy of all of them in
// every instance, so it asks a Set which families a source needs and loads only
// those.
package fallbackfont

import (
	"embed"
	"path"
	"sync"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/fontfile"
	"golang.org/x/image/font/sfnt"
)

//go:embed fonts/*.ttf
var fontFS embed.FS

const dir = "fonts"

// Family is one fallback family: its name and its faces (Regular, and Bold
// where the script has one). typst ranks fallback candidates by family name
// before weight, so the faces must be added under this one name; a Bold added
// as a family of its own could win over the Regular in plain text.
type Family struct {
	Name  string
	Faces [][]byte
}

// File is one embedded face, by file name.
type File struct {
	Name string
	Data []byte
}

// Files lists every embedded face, in file-name order.
func Files() []File {
	entries, err := fontFS.ReadDir(dir)
	if err != nil {
		panic(err) // embedded at build time
	}
	out := make([]File, 0, len(entries))
	for _, e := range entries {
		data, err := fontFS.ReadFile(path.Join(dir, e.Name()))
		if err != nil {
			panic(err)
		}
		out = append(out, File{Name: e.Name(), Data: data})
	}
	return out
}

type parsedFamily struct {
	Family
	first *sfnt.Font // the face coverage is judged by
}

var families = sync.OnceValue(func() []parsedFamily {
	var out []parsedFamily
	byName := map[string]int{}
	for _, f := range Files() {
		name := fontfile.FamilyOf(f.Data)
		i, ok := byName[name]
		if !ok {
			parsed, err := sfnt.Parse(f.Data)
			if err != nil {
				panic(err) // embedded at build time
			}
			i = len(out)
			byName[name] = i
			out = append(out, parsedFamily{Family: Family{Name: name}, first: parsed})
		}
		out[i].Faces = append(out[i].Faces, f.Data)
	}
	return out
})

// Set answers which fallback families a text needs, given the faces that are
// always loaded. It is safe for concurrent use.
type Set struct {
	base []*sfnt.Font

	mu    sync.Mutex
	cache map[rune]int // the family that covers the rune, or none
	buf   sfnt.Buffer
}

const none = -1

// asciiEnd: ASCII is most of a typst source, and every base face has it.
const asciiEnd = 0x80

// NewSet makes a Set over the always-loaded faces; a rune any of them has
// needs no fallback. A face that does not parse is left out.
func NewSet(base [][]byte) *Set {
	s := &Set{cache: map[rune]int{}}
	for _, b := range base {
		if f, err := sfnt.Parse(b); err == nil {
			s.base = append(s.base, f)
		}
	}
	return s
}

// For returns the families text has a character of that no base face has,
// each once. Text the base faces cover whole, nearly every package, needs
// none.
func (s *Set) For(text string) []Family {
	fams := families()
	var out []Family
	seen := map[int]bool{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range text {
		if r < asciiEnd {
			continue
		}
		i, ok := s.cache[r]
		if !ok {
			i = s.familyFor(fams, r)
			s.cache[r] = i
		}
		if i != none && !seen[i] {
			seen[i] = true
			out = append(out, fams[i].Family)
		}
	}
	return out
}

func (s *Set) familyFor(fams []parsedFamily, r rune) int {
	for _, f := range s.base {
		if s.has(f, r) {
			return none
		}
	}
	for i, f := range fams {
		if s.has(f.first, r) {
			return i
		}
	}
	return none
}

func (s *Set) has(f *sfnt.Font, r rune) bool {
	gi, err := f.GlyphIndex(&s.buf, r)
	return err == nil && gi != 0
}
