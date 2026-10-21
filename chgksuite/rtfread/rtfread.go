// Package rtfread turns an .rtf file into plain text, a paragraph per line, for
// reading what a packet says (credits). It keeps the body text and drops
// everything else: font and colour tables, styles, document info, pictures,
// objects, headers and footers, field instructions and every \* destination.
// Bytes written as \'hh are decoded in the code page of the font they stand in
// (\fcharset), else the document's \ansicpg; \uN characters are taken as they
// are and their fallback bytes skipped.
package rtfread

import (
	"bytes"
	"errors"
	"strconv"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// ErrNotRTF is a file that does not open with {\rtf.
var ErrNotRTF = errors.New("not an RTF file")

// skipped are the destinations whose text is not part of the body.
var skipped = map[string]bool{
	"fonttbl": true, "colortbl": true, "stylesheet": true, "info": true, "pict": true,
	"object": true, "header": true, "headerl": true, "headerr": true, "headerf": true,
	"footer": true, "footerl": true, "footerr": true, "footerf": true, "fldinst": true,
	"listtable": true, "listoverridetable": true, "revtbl": true, "rsidtbl": true,
	"generator": true, "themedata": true, "colorschememapping": true, "latentstyles": true,
	"datastore": true, "xmlnstbl": true, "filetbl": true, "pgdsctbl": true, "footnote": true,
	"bkmkstart": true, "bkmkend": true, "shpinst": true, "nonshppict": true, "datafield": true,
}

// codePages maps an \fcharset to its Windows code page.
var codePages = map[int]int{0: 1252, 161: 1253, 162: 1254, 177: 1255, 178: 1256, 186: 1257, 204: 1251, 238: 1250}

var decoders = map[int]*charmap.Charmap{
	1250: charmap.Windows1250, 1251: charmap.Windows1251, 1252: charmap.Windows1252, 1253: charmap.Windows1253,
	1254: charmap.Windows1254, 1255: charmap.Windows1255, 1256: charmap.Windows1256, 1257: charmap.Windows1257,
	866: charmap.CodePage866, 10007: charmap.MacintoshCyrillic,
}

type state struct {
	skip bool
	uc   int // fallback characters after \uN
	font int
	// inFontTable: \fN and \fcharsetN describe a font rather than switch to it.
	inFontTable bool
}

type reader struct {
	src       []byte
	pos       int
	out       strings.Builder
	pending   []byte // \'hh bytes not yet decoded
	ansicpg   int
	charsets  map[int]int // font → code page
	tableFont int
	st        state
	stack     []state
	toSkip    int // fallback characters still to drop after \uN
}

// ToText reads an RTF document's body text.
func ToText(data []byte) (string, error) {
	if !bytes.HasPrefix(bytes.TrimLeft(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), " \r\n\t"), []byte(`{\rtf`)) {
		return "", ErrNotRTF
	}
	r := &reader{src: data, ansicpg: 1252, charsets: map[int]int{}, st: state{uc: 1, font: -1}}
	r.run()
	r.flush()
	return r.out.String(), nil
}

func (r *reader) run() {
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		r.pos++
		switch c {
		case '{':
			r.stack = append(r.stack, r.st)
			if r.pos+1 < len(r.src) && r.src[r.pos] == '\\' && r.src[r.pos+1] == '*' {
				r.st.skip = true
			}
		case '}':
			if n := len(r.stack); n > 0 {
				r.flush()
				r.st = r.stack[n-1]
				r.stack = r.stack[:n-1]
			}
		case '\\':
			r.control()
		case '\r', '\n':
		default:
			r.text(c)
		}
	}
}

func (r *reader) control() {
	if r.pos >= len(r.src) {
		return
	}
	c := r.src[r.pos]
	if !isLetter(c) {
		r.pos++
		switch c {
		case '\'':
			if r.pos+2 <= len(r.src) {
				if b, err := strconv.ParseUint(string(r.src[r.pos:r.pos+2]), 16, 8); err == nil {
					r.pos += 2
					r.byteChar(byte(b))
				}
			}
		case '\\', '{', '}':
			r.text(c)
		case '~':
			r.emit(" ")
		case '_':
			r.emit("-")
		case '\r', '\n':
			r.emit("\n")
		}
		return
	}
	start := r.pos
	for r.pos < len(r.src) && isLetter(r.src[r.pos]) {
		r.pos++
	}
	word := string(r.src[start:r.pos])
	numStart := r.pos
	if r.pos < len(r.src) && r.src[r.pos] == '-' {
		r.pos++
	}
	for r.pos < len(r.src) && r.src[r.pos] >= '0' && r.src[r.pos] <= '9' {
		r.pos++
	}
	num := 0
	if r.pos > numStart {
		num, _ = strconv.Atoi(string(r.src[numStart:r.pos]))
	}
	if r.pos < len(r.src) && r.src[r.pos] == ' ' {
		r.pos++
	}
	r.word(word, num)
}

func (r *reader) word(word string, num int) {
	if skipped[word] {
		if word == "fonttbl" {
			r.st.inFontTable = true
		} else {
			r.st.skip = true
		}
		return
	}
	switch word {
	case "bin":
		r.pos = min(len(r.src), r.pos+max(num, 0))
	case "ansicpg":
		r.ansicpg = num
	case "uc":
		r.st.uc = num
	case "u":
		if num < 0 {
			num += 0x10000
		}
		r.emit(string(rune(num)))
		r.toSkip = r.st.uc
	case "f":
		if r.st.inFontTable {
			r.tableFont = num
		} else {
			r.flush()
			r.st.font = num
		}
	case "fcharset":
		if r.st.inFontTable {
			if cp, ok := codePages[num]; ok {
				r.charsets[r.tableFont] = cp
			}
		}
	case "cpg":
		if r.st.inFontTable {
			r.charsets[r.tableFont] = num
		}
	case "par", "line", "sect", "page", "row":
		r.emit("\n")
	case "tab", "cell":
		r.emit("\t")
	case "emdash":
		r.emit("—")
	case "endash":
		r.emit("–")
	case "lquote":
		r.emit("‘")
	case "rquote":
		r.emit("’")
	case "ldblquote":
		r.emit("“")
	case "rdblquote":
		r.emit("”")
	case "bullet":
		r.emit("•")
	}
}

func (r *reader) text(c byte) {
	if r.toSkip > 0 {
		r.toSkip--
		return
	}
	if r.st.skip || r.st.inFontTable {
		return
	}
	r.flush()
	r.out.WriteByte(c)
}

func (r *reader) byteChar(b byte) {
	if r.toSkip > 0 {
		r.toSkip--
		return
	}
	if r.st.skip || r.st.inFontTable {
		return
	}
	r.pending = append(r.pending, b)
}

func (r *reader) emit(s string) {
	if r.st.skip || r.st.inFontTable {
		return
	}
	r.flush()
	r.out.WriteString(s)
}

// flush decodes the \'hh bytes gathered so far in the current font's code page.
func (r *reader) flush() {
	if len(r.pending) == 0 {
		return
	}
	cp := r.ansicpg
	if c, ok := r.charsets[r.st.font]; ok {
		cp = c
	}
	var dec encoding.Encoding = charmap.Windows1252
	if d, ok := decoders[cp]; ok {
		dec = d
	}
	s, err := dec.NewDecoder().Bytes(r.pending)
	if err == nil {
		r.out.Write(s)
	}
	r.pending = r.pending[:0]
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
