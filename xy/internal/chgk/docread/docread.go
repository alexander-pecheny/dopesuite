// Package docread reads the plain text of a Word 97–2003 .doc — the binary
// format before .docx — so `chgksuite credits` can read old packets without
// LibreOffice. It is the minimum of [MS-DOC] that gives the main document's
// text in reading order: the OLE container (cfb.go), the FIB, and the piece
// table. Formatting, lists, images, footnotes and headers are not read, and
// neither are Word 6/95 or encrypted files. LibreOffice's ww8 filter
// (sw/source/filter/ww8/ww8scan.cxx) is the reference for what real files do.
package docread

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"

	"xy/internal/chgk/docxread"
	"xy/internal/chgk/typo"
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
	c, err := openCFB(data)
	if err != nil {
		return "", err
	}
	wd, _, err := c.stream("WordDocument")
	if err != nil {
		return "", err
	}
	if len(wd) < 0x22 {
		return "", fmt.Errorf("doc: no WordDocument stream")
	}
	le := binary.LittleEndian
	switch ident := le.Uint16(wd); {
	case ident == 0xA5DC || ident == 0xA59B:
		return "", ErrOldWord
	case ident != 0xA5EC:
		return "", fmt.Errorf("doc: unknown FIB identifier %#x", ident)
	}
	if nFib := le.Uint16(wd[2:]); nFib < 0x6A {
		return "", ErrOldWord
	}
	flags := le.Uint16(wd[0x0A:])
	if flags&0x0100 != 0 {
		return "", ErrEncrypted
	}

	// The FIB's variable part: csw shorts, cslw longs (ccpText is the fourth),
	// then cbRgFcLcb pairs of fc/lcb, of which fcClx/lcbClx is the 34th.
	csw := int(le.Uint16(wd[0x20:]))
	p := 0x22 + 2*csw
	if p+2 > len(wd) {
		return "", fmt.Errorf("doc: truncated FIB")
	}
	cslw := int(le.Uint16(wd[p:]))
	rglw := p + 2
	p = rglw + 4*cslw
	if cslw < 4 || p+2 > len(wd) {
		return "", fmt.Errorf("doc: truncated FIB")
	}
	ccpText := le.Uint32(wd[rglw+12:])
	if ccpText > uint32(len(wd)) {
		// Every character takes at least a byte of the stream; pieces may
		// overlap, so without this cap a crafted file could ask for gigabytes.
		return "", fmt.Errorf("doc: text longer than the document")
	}
	cbRgFcLcb := int(le.Uint16(wd[p:]))
	fcLcb := p + 2
	if cbRgFcLcb < 34 || fcLcb+34*8 > len(wd) {
		return "", fmt.Errorf("doc: truncated FIB")
	}
	fcClx := le.Uint32(wd[fcLcb+33*8:])
	lcbClx := le.Uint32(wd[fcLcb+33*8+4:])

	tableName := "0Table"
	if flags&0x0200 != 0 {
		tableName = "1Table"
	}
	table, ok, err := c.stream(tableName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("doc: no %s stream", tableName)
	}
	if uint64(fcClx)+uint64(lcbClx) > uint64(len(table)) || lcbClx == 0 {
		return "", fmt.Errorf("doc: piece table out of range")
	}
	raw, err := readPieces(wd, table[fcClx:fcClx+lcbClx], ccpText)
	if err != nil {
		return "", err
	}
	return docxread.Tidy(render(raw)), nil
}

// readPieces assembles the first ccpText characters through the piece table
// (the Clx: optional Prc records of property modifiers, then the Pcdt). A
// piece is UTF-16LE, or, with bit 30 of its fc set, one byte per character in
// cp1252 at fc/2.
func readPieces(wd, clx []byte, ccpText uint32) ([]rune, error) {
	le := binary.LittleEndian
	for len(clx) > 0 && clx[0] == 0x01 {
		if len(clx) < 3 {
			return nil, fmt.Errorf("doc: truncated Clx")
		}
		n := 3 + int(le.Uint16(clx[1:]))
		if n > len(clx) {
			return nil, fmt.Errorf("doc: truncated Clx")
		}
		clx = clx[n:]
	}
	if len(clx) < 5 || clx[0] != 0x02 {
		return nil, fmt.Errorf("doc: no piece table")
	}
	lcb := int(le.Uint32(clx[1:]))
	plc := clx[5:]
	if lcb > len(plc) || (lcb-4)%12 != 0 {
		return nil, fmt.Errorf("doc: bad piece table size")
	}
	n := (lcb - 4) / 12
	cp := func(i int) uint32 { return le.Uint32(plc[4*i:]) }
	pcd := plc[4*(n+1):]
	dec := charmap.Windows1252.NewDecoder()

	var out []rune
	for i := 0; i < n && uint32(len(out)) < ccpText; i++ {
		start, end := cp(i), cp(i+1)
		if end <= start {
			continue
		}
		count := end - start
		if left := ccpText - uint32(len(out)); count > left {
			count = left
		}
		fc := le.Uint32(pcd[8*i+2:])
		if fc&0x40000000 != 0 {
			off := uint64(fc&^0x40000000) / 2
			if off+uint64(count) > uint64(len(wd)) {
				return nil, fmt.Errorf("doc: piece past the end of the text")
			}
			s, err := dec.Bytes(wd[off : off+uint64(count)])
			if err != nil {
				return nil, err
			}
			out = append(out, []rune(string(s))...)
		} else {
			off := uint64(fc)
			if off+2*uint64(count) > uint64(len(wd)) {
				return nil, fmt.Errorf("doc: piece past the end of the text")
			}
			u := make([]uint16, count)
			for k := range u {
				u[k] = le.Uint16(wd[off+2*uint64(k):])
			}
			out = append(out, utf16.Decode(u)...)
		}
	}
	return out, nil
}

// render turns Word's control characters into docxread's plain text.
func render(raw []rune) string {
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
	for _, r := range raw {
		switch r {
		case 0x13:
			inCode = append(inCode, true)
			continue
		case 0x14:
			if len(inCode) > 0 {
				inCode[len(inCode)-1] = false
			}
			continue
		case 0x15:
			if len(inCode) > 0 {
				inCode = inCode[:len(inCode)-1]
			}
			continue
		}
		if hidden() {
			continue
		}
		switch r {
		case '\r', 0x07, 0x0C: // paragraph, table cell or row end, page or section break
			flush()
		case 0x0B: // manual line break
			cur.WriteByte('\n')
		case 0x1E: // non-breaking hyphen
			cur.WriteByte('-')
		case 0x1F: // optional hyphen
		case 0x09:
			cur.WriteByte('\t')
		default:
			// Below 0x20 are anchors with nothing to read: a picture (0x01),
			// a footnote or comment mark (0x02, 0x05), a drawn object (0x08).
			if r >= 0x20 {
				cur.WriteRune(r)
			}
		}
	}
	if cur.Len() > 0 {
		flush()
	}
	return strings.Join(paras, "\n\n")
}
