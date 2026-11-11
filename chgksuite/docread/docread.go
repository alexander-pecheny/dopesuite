// Package docread reads the plain text of a Word 97–2003 .doc — the binary
// format before .docx — so `chgksuite credits` can read old packets without
// LibreOffice. It is the minimum of [MS-DOC] that gives the main document's
// text in reading order: the OLE container (cfb.go), the FIB, and the piece
// table; ToTextImages also reads the pictures (pictures.go). Formatting,
// lists, footnotes and headers are not read, and neither are Word 6/95 or
// encrypted files. LibreOffice's ww8 filter
// (sw/source/filter/ww8/ww8scan.cxx) is the reference for what real files do.
package docread

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/docxread"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/typo"
)

// FIB layout ([MS-DOC] 2.5.1).
const (
	fibRgW        = 0x22 // where the variable part starts, after FibBase and csw
	identWord6    = 0xA5DC
	identWord95   = 0xA59B
	identWord97   = 0xA5EC
	minNFib       = 0x6A // the oldest nFib with the Word 97 layout
	flagEncrypted = 0x0100
	flagWhichTbl  = 0x0200 // the piece table is in 1Table, not 0Table
	longSize      = 4
	ccpTextLong   = 3 // ccpText is the fourth entry of FibRgLw97
	fcLcbPairSize = 8
	clxPair       = 33 // fcClx/lcbClx is the 34th pair of FibRgFcLcb97
)

// Clx and piece table layout ([MS-DOC] 2.9.38).
const (
	clxtPrc      = 0x01
	clxtPcdt     = 0x02
	prcHeaderLen = 3 // clxt plus cbGrpprl
	pcdtHeadLen  = 5 // clxt plus lcb
	cpSize       = 4
	pcdSize      = 8
	fcCompressed = 1 << 30 // the piece is cp1252 at fc/2
)

// Control characters in the text stream.
const (
	fieldBegin     = 0x13
	fieldSeparator = 0x14
	fieldEnd       = 0x15
	cellMark       = 0x07 // table cell or row end
	pageBreak      = 0x0C // page or section break
	lineBreak      = 0x0B
	nbHyphen       = 0x1E
	softHyphen     = 0x1F
)

var (
	// ErrEncrypted is a password-protected document.
	ErrEncrypted = errors.New("the .doc is password-protected")
	// ErrOldWord is a Word 6/95 (or older) document, which has no piece table
	// in the 97 layout and keeps 8-bit text in the document's codepage.
	ErrOldWord = errors.New("Word 6/95 .doc files are not supported")
)

// ToText returns the document's main text as docxread.ToText would give it for
// the same document saved as .docx, minus images, list numbers and formatting:
// paragraphs separated by a blank line, table cells as paragraphs of their own,
// a manual line break as a newline, field codes dropped and their results kept.
func ToText(data []byte) (string, error) {
	d, err := open(data)
	if err != nil {
		return "", err
	}
	return docxread.Tidy(render(d.raw, nil)), nil
}

// document is a .doc opened down to its main text: raw holds a rune per
// character position (CP), fcs the stream offset each one was read from.
type document struct {
	c         *cfb
	wd, table []byte
	fcLcb     int // where the FIB's fc/lcb pairs start
	cbRgFcLcb int
	raw       []rune
	fcs       []uint32
}

func open(data []byte) (*document, error) {
	c, err := openCFB(data)
	if err != nil {
		return nil, err
	}
	wd, _, err := c.stream("WordDocument")
	if err != nil {
		return nil, err
	}
	if len(wd) < fibRgW {
		return nil, fmt.Errorf("doc: no WordDocument stream")
	}
	le := binary.LittleEndian
	switch ident := le.Uint16(wd); {
	case ident == identWord6 || ident == identWord95:
		return nil, ErrOldWord
	case ident != identWord97:
		return nil, fmt.Errorf("doc: unknown FIB identifier %#x", ident)
	}
	if nFib := le.Uint16(wd[2:]); nFib < minNFib {
		return nil, ErrOldWord
	}
	flags := le.Uint16(wd[0x0A:])
	if flags&flagEncrypted != 0 {
		return nil, ErrEncrypted
	}

	// The FIB's variable part: csw shorts, cslw longs (ccpText is the fourth),
	// then cbRgFcLcb pairs of fc/lcb, of which fcClx/lcbClx is the 34th.
	csw := int(le.Uint16(wd[0x20:]))
	p := fibRgW + 2*csw
	if p+2 > len(wd) {
		return nil, fmt.Errorf("doc: truncated FIB")
	}
	cslw := int(le.Uint16(wd[p:]))
	rglw := p + 2
	p = rglw + longSize*cslw
	if cslw <= ccpTextLong || p+2 > len(wd) {
		return nil, fmt.Errorf("doc: truncated FIB")
	}
	ccpText := le.Uint32(wd[rglw+ccpTextLong*longSize:])
	if ccpText > uint32(len(wd)) {
		// Every character takes at least a byte of the stream; pieces may
		// overlap, so without this cap a crafted file could ask for gigabytes.
		return nil, fmt.Errorf("doc: text longer than the document")
	}
	cbRgFcLcb := int(le.Uint16(wd[p:]))
	fcLcb := p + 2
	if cbRgFcLcb <= clxPair || fcLcb+(clxPair+1)*fcLcbPairSize > len(wd) {
		return nil, fmt.Errorf("doc: truncated FIB")
	}

	tableName := "0Table"
	if flags&flagWhichTbl != 0 {
		tableName = "1Table"
	}
	table, ok, err := c.stream(tableName)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("doc: no %s stream", tableName)
	}
	d := &document{c: c, wd: wd, table: table, fcLcb: fcLcb, cbRgFcLcb: cbRgFcLcb}
	clx, ok := d.tableBlock(clxPair)
	if !ok || len(clx) == 0 {
		return nil, fmt.Errorf("doc: piece table out of range")
	}
	if d.raw, d.fcs, err = readPieces(wd, clx, ccpText); err != nil {
		return nil, err
	}
	return d, nil
}

// tableBlock is the table stream's block the FIB's fc/lcb pair i names.
func (d *document) tableBlock(i int) ([]byte, bool) {
	if i >= d.cbRgFcLcb || d.fcLcb+(i+1)*fcLcbPairSize > len(d.wd) {
		return nil, false
	}
	le := binary.LittleEndian
	fc := le.Uint32(d.wd[d.fcLcb+i*fcLcbPairSize:])
	lcb := le.Uint32(d.wd[d.fcLcb+i*fcLcbPairSize+longSize:])
	if uint64(fc)+uint64(lcb) > uint64(len(d.table)) {
		return nil, false
	}
	return d.table[fc : fc+lcb], true
}

// readPieces assembles the first ccpText characters through the piece table
// (the Clx: optional Prc records of property modifiers, then the Pcdt). A
// piece is UTF-16LE, or, with bit 30 of its fc set, one byte per character in
// cp1252 at fc/2.
func readPieces(wd, clx []byte, ccpText uint32) ([]rune, []uint32, error) {
	le := binary.LittleEndian
	for len(clx) > 0 && clx[0] == clxtPrc {
		if len(clx) < prcHeaderLen {
			return nil, nil, fmt.Errorf("doc: truncated Clx")
		}
		n := prcHeaderLen + int(le.Uint16(clx[1:]))
		if n > len(clx) {
			return nil, nil, fmt.Errorf("doc: truncated Clx")
		}
		clx = clx[n:]
	}
	if len(clx) < pcdtHeadLen || clx[0] != clxtPcdt {
		return nil, nil, fmt.Errorf("doc: no piece table")
	}
	lcb := int(le.Uint32(clx[1:]))
	plc := clx[pcdtHeadLen:]
	if lcb > len(plc) || (lcb-cpSize)%(cpSize+pcdSize) != 0 {
		return nil, nil, fmt.Errorf("doc: bad piece table size")
	}
	n := (lcb - cpSize) / (cpSize + pcdSize)
	cp := func(i int) uint32 { return le.Uint32(plc[cpSize*i:]) }
	pcd := plc[cpSize*(n+1):]
	dec := charmap.Windows1252.NewDecoder()

	var out []rune
	var fcs []uint32
	for i := 0; i < n && uint32(len(out)) < ccpText; i++ {
		start, end := cp(i), cp(i+1)
		if end <= start {
			continue
		}
		count := end - start
		if left := ccpText - uint32(len(out)); count > left {
			count = left
		}
		fc := le.Uint32(pcd[pcdSize*i+2:])
		if fc&fcCompressed != 0 {
			off := uint64(fc&^fcCompressed) / 2
			if off+uint64(count) > uint64(len(wd)) {
				return nil, nil, fmt.Errorf("doc: piece past the end of the text")
			}
			s, err := dec.Bytes(wd[off : off+uint64(count)])
			if err != nil {
				return nil, nil, err
			}
			rs := []rune(string(s))
			if len(rs) != int(count) {
				return nil, nil, fmt.Errorf("doc: a compressed piece of %d bytes decoded to %d characters", count, len(rs))
			}
			out = append(out, rs...)
			for k := range uint64(count) {
				fcs = append(fcs, uint32(off+k))
			}
		} else {
			off := uint64(fc)
			if off+2*uint64(count) > uint64(len(wd)) {
				return nil, nil, fmt.Errorf("doc: piece past the end of the text")
			}
			// One rune per UTF-16 unit, so a rune's index is its CP: a
			// surrogate pair gives its character and a 0, which render drops.
			for k := uint64(0); k < uint64(count); k++ {
				u := le.Uint16(wd[off+2*k:])
				r := rune(u)
				if utf16.IsSurrogate(r) && k+1 < uint64(count) {
					if pair := utf16.DecodeRune(r, rune(le.Uint16(wd[off+2*k+2:]))); pair != utf8.RuneError {
						out = append(out, pair, 0)
						fcs = append(fcs, uint32(off+2*k), uint32(off+2*k+2))
						k++
						continue
					}
				}
				if utf16.IsSurrogate(r) {
					r = utf8.RuneError
				}
				out = append(out, r)
				fcs = append(fcs, uint32(off+2*k))
			}
		}
	}
	return out, fcs, nil
}

// render turns Word's control characters into docxread's plain text. pictures
// keys the picture anchors (0x01 inline, 0x08 floating) by their CP: each
// becomes a marker of its key, which ToTextImages swaps for the directive.
func render(raw []rune, pictures map[int]string) string {
	var paras []string
	var cur strings.Builder
	// Fields nest: 0x13 opens one, 0x14 ends its code and starts its result,
	// 0x15 closes it. inCode is, per open field, whether we are still in the
	// code (hidden) part.
	var inCode []bool
	hidden := func() bool {
		for _, c := range inCode {
			if c {
				return true
			}
		}
		return false
	}
	flush := func() {
		// As docxread does: an underscore is 4s's italic mark, so a literal
		// one is escaped.
		paras = append(paras, typo.EscapeUnderscoresExceptURLs(strings.TrimRight(cur.String(), " "), false))
		cur.Reset()
	}
	for i, r := range raw {
		switch r {
		case fieldBegin:
			inCode = append(inCode, true)
			continue
		case fieldSeparator:
			if len(inCode) > 0 {
				inCode[len(inCode)-1] = false
			}
			continue
		case fieldEnd:
			if len(inCode) > 0 {
				inCode = inCode[:len(inCode)-1]
			}
			continue
		}
		if hidden() {
			continue
		}
		switch r {
		case '\r', cellMark, pageBreak:
			flush()
		case lineBreak:
			cur.WriteByte('\n')
		case nbHyphen:
			cur.WriteByte('-')
		case softHyphen:
		case '\t':
			cur.WriteByte('\t')
		default:
			// Below 0x20 are anchors: a picture (0x01), a footnote or comment
			// mark (0x02, 0x05), a drawn object (0x08). A picture read
			// (ToTextImages) gets a marker, swapped for its directive after the
			// underscores are escaped: the name has its own.
			if key, ok := pictures[i]; ok {
				cur.WriteString(pictureMark + key + pictureMarkEnd)
			} else if r >= ' ' {
				cur.WriteRune(r)
			}
		}
	}
	if cur.Len() > 0 {
		flush()
	}
	return strings.Join(paras, "\n\n")
}
