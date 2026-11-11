// Package rtfread turns an .rtf file into plain text, a paragraph per line, for
// reading what a packet says (credits). It keeps the body text and drops
// everything else: font and colour tables, styles, document info, pictures,
// objects, headers and footers, field instructions and every \* destination.
// Bytes written as \'hh are decoded in the code page of the font they stand in
// (\fcharset), else the document's \ansicpg; \uN characters are taken as they
// are and their fallback bytes skipped. ToTextImages also keeps the body's
// PNG and JPEG pictures, each as an (img …) directive where it stands.
package rtfread

import (
	"bytes"
	"encoding/hex"
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
	// hidden: inside a destination that is not the body (a header, the info, a
	// \* destination other than \shppict); a picture there is not kept.
	hidden bool
	// pict: inside a \pict group, whose own text is the picture's hex;
	// pictNested: in a group within it (\*\blipuid), whose text is not.
	pict, pictNested bool
}

// Image is a picture of the body, named as its (img …) directive names it.
type Image struct {
	Name string
	Data []byte
}

// picture is the \pict group being read.
type picture struct {
	ext string // "png" or "jpg"; "" for a format a browser does not draw (WMF, EMF, DIB)
	hex []byte
	bin []byte
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
	// images: keep pictures (ToTextImages), named prefix + "imageN.ext".
	images bool
	prefix string
	pic    *picture
	pics   []Image
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

// ToTextImages is ToText with the body's PNG and JPEG pictures: each becomes
// "(img <prefix>imageN.png)" in the text where it stands, and an Image of that
// name. A picture in a header, a footnote or an object is left out, and so is
// one in a format a browser does not draw (a Word picture's WMF fallback in
// \nonshppict is skipped with its group).
func ToTextImages(data []byte, prefix string) (string, []Image, error) {
	if !bytes.HasPrefix(bytes.TrimLeft(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), " \r\n\t"), []byte(`{\rtf`)) {
		return "", nil, ErrNotRTF
	}
	r := &reader{src: data, ansicpg: 1252, charsets: map[int]int{}, st: state{uc: 1, font: -1}, images: true, prefix: prefix}
	r.run()
	r.flush()
	return r.out.String(), r.pics, nil
}

func (r *reader) run() {
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		r.pos++
		switch c {
		case '{':
			r.stack = append(r.stack, r.st)
			if r.st.pict {
				r.st.pictNested = true
			}
			if r.pos+1 < len(r.src) && r.src[r.pos] == '\\' && r.src[r.pos+1] == '*' {
				r.st.skip = true
				// \*\shppict holds the body's picture; any other \* destination is not body.
				if !bytes.HasPrefix(r.src[r.pos+2:], []byte(`\shppict`)) {
					r.st.hidden = true
				}
			}
		case '}':
			if n := len(r.stack); n > 0 {
				r.flush()
				closed := r.st
				r.st = r.stack[n-1]
				r.stack = r.stack[:n-1]
				if closed.pict && !r.st.pict {
					r.endPicture(closed)
				}
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
	if word == "pict" && r.images {
		r.st.skip, r.st.pict = true, true
		r.pic = &picture{}
		return
	}
	if skipped[word] {
		if word == "fonttbl" {
			r.st.inFontTable = true
		} else {
			r.st.skip, r.st.hidden = true, true
		}
		return
	}
	if r.st.pict {
		if r.st.pictNested {
			return
		}
		switch word {
		case "pngblip":
			r.pic.ext = "png"
		case "jpegblip":
			r.pic.ext = "jpg"
		case "bin":
			end := min(len(r.src), r.pos+max(num, 0))
			r.pic.bin = append(r.pic.bin, r.src[r.pos:end]...)
			r.pos = end
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
	if r.st.pict {
		if isHex(c) && !r.st.pictNested {
			r.pic.hex = append(r.pic.hex, c)
		}
		return
	}
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

// endPicture keeps the \pict group just closed when it is a body picture a
// browser draws, and writes its directive where it stood.
func (r *reader) endPicture(st state) {
	pic := r.pic
	r.pic = nil
	if pic == nil || pic.ext == "" || st.hidden || r.st.inFontTable {
		return
	}
	data := pic.bin
	if len(data) == 0 {
		if len(pic.hex)%2 == 1 {
			pic.hex = pic.hex[:len(pic.hex)-1]
		}
		data = make([]byte, len(pic.hex)/2)
		if _, err := hex.Decode(data, pic.hex); err != nil {
			return
		}
	}
	if len(data) == 0 {
		return
	}
	name := r.prefix + "image" + strconv.Itoa(len(r.pics)+1) + "." + pic.ext
	r.pics = append(r.pics, Image{Name: name, Data: data})
	r.out.WriteString("(img " + name + ")")
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
