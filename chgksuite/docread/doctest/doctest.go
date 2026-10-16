// Package doctest writes minimal Word 97 .doc files for tests: an OLE compound
// file holding a WordDocument stream with a FIB and the text, and a 1Table
// stream (small enough for the mini stream) with the piece table.
package doctest

import (
	"encoding/binary"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

// FIB values of a Word 97 document.
const (
	identWord97   = 0xA5EC
	nFibWord97    = 0xC1
	flagWhichTbl  = 0x0200 // fWhichTableStm: 1Table
	flagEncrypted = 0x0100
	cswWord97     = 14 // shorts in FibRgW97
	cslwWord97    = 22 // longs in FibRgLw97
	cbRgFcLcb97   = 93 // fc/lcb pairs in FibRgFcLcb97
	fcCompressed  = 1 << 30
	clxOffset     = 16 // fcClx points past some other structure
	clxtPcdt      = 0x02
	// prcRecord is one Prc with two bytes of property modifiers, for the
	// reader to skip.
	prcRecord = "\x01\x02\x00\xAA\xBB"
)

// Compound file layout, version 3.
const (
	cfbSignature   = "\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1"
	minorVersion   = 0x3E
	majorVersion   = 3
	byteOrderMark  = 0xFFFE
	sectorShift    = 9
	miniShift      = 6
	miniSectorSize = 1 << miniShift
	miniCutoff     = 4096 // smaller streams live in the mini stream
	sectorIDSize   = 4
	idsPerSector   = 512 / sectorIDSize
	dirEntrySize   = 128
	headerDIFATLen = 109
	headerDIFAT    = 0x4C
	typeStream     = 2
	typeRoot       = 5
)

// Piece is one run of text. Compressed stores it one byte per character in
// cp1252, as Word does for text that fits; otherwise it is UTF-16LE.
type Piece struct {
	Text       string
	Compressed bool
}

// Options shape the file beyond its text.
type Options struct {
	// Main is how many characters are the main document (ccpText); the rest
	// stands for footnotes and headers. 0 means all of them.
	Main int
	// Encrypted sets fEncrypted.
	Encrypted bool
	// Ident overrides wIdent (0xA5DC is Word 6/95).
	Ident uint16
}

// Doc is a document holding text as one UTF-16 piece.
func Doc(text string) []byte {
	return Pieces([]Piece{{Text: text}}, Options{})
}

// Pieces is a document whose text is the pieces in order. They are stored in
// the WordDocument stream in reverse, as a fast save leaves them, so a reader
// that ignores the piece table reads them out of order.
func Pieces(pieces []Piece, opt Options) []byte {
	le := binary.LittleEndian
	const textStart = 0x400
	wd := make([]byte, textStart)
	fcs := make([]uint32, len(pieces))
	cps := []uint32{0}
	for i := len(pieces) - 1; i >= 0; i-- {
		p := pieces[i]
		if p.Compressed {
			b, err := charmap.Windows1252.NewEncoder().Bytes([]byte(p.Text))
			if err != nil {
				panic(err)
			}
			fcs[i] = uint32(len(wd))*2 | fcCompressed
			wd = append(wd, b...)
		} else {
			fcs[i] = uint32(len(wd))
			for _, u := range utf16.Encode([]rune(p.Text)) {
				wd = le.AppendUint16(wd, u)
			}
		}
	}
	for _, p := range pieces {
		cps = append(cps, cps[len(cps)-1]+uint32(len([]rune(p.Text))))
	}
	ccp := cps[len(cps)-1]
	if opt.Main > 0 {
		ccp = uint32(opt.Main)
	}

	// The Clx: one Prc (to be skipped), then the Pcdt.
	var plc []byte
	for _, cp := range cps {
		plc = le.AppendUint32(plc, cp)
	}
	for _, fc := range fcs {
		plc = le.AppendUint16(plc, 0)
		plc = le.AppendUint32(plc, fc)
		plc = le.AppendUint16(plc, 0)
	}
	table := make([]byte, clxOffset)
	table = append(table, prcRecord...)
	table = append(table, clxtPcdt)
	table = le.AppendUint32(table, uint32(len(plc)))
	table = append(table, plc...)

	ident := opt.Ident
	if ident == 0 {
		ident = identWord97
	}
	le.PutUint16(wd[0:], ident)
	le.PutUint16(wd[2:], nFibWord97)
	flags := uint16(flagWhichTbl)
	if opt.Encrypted {
		flags |= flagEncrypted
	}
	le.PutUint16(wd[0x0A:], flags)
	le.PutUint16(wd[0x20:], cswWord97)
	le.PutUint16(wd[0x3E:], cslwWord97)
	le.PutUint32(wd[0x4C:], ccp)
	le.PutUint16(wd[0x98:], cbRgFcLcb97)
	le.PutUint32(wd[0x1A2:], clxOffset)
	le.PutUint32(wd[0x1A6:], uint32(len(table)-clxOffset))

	for len(wd) < miniCutoff {
		wd = append(wd, 0) // past the mini stream cutoff
	}
	return compound(wd, table)
}

// compound writes a version 3 compound file: FAT, directory, mini FAT, the mini
// stream (holding table) and the WordDocument stream, in that sector order.
func compound(wd, table []byte) []byte {
	le := binary.LittleEndian
	const ss = 512
	const end, free, fatSect, none = 0xFFFFFFFE, 0xFFFFFFFF, 0xFFFFFFFD, 0xFFFFFFFF
	sectors := func(n int) int { return (n + ss - 1) / ss }

	mini := append([]byte(nil), table...)
	for len(mini)%miniSectorSize != 0 {
		mini = append(mini, 0)
	}
	nMini, nWD := sectors(len(mini)), sectors(len(wd))
	nFAT := 1
	for nFAT*idsPerSector < nFAT+2+nMini+nWD {
		nFAT++
	}
	dirAt := uint32(nFAT)
	miniFATAt := dirAt + 1
	miniAt := miniFATAt + 1
	wdAt := miniAt + uint32(nMini)
	total := int(wdAt) + nWD

	fat := make([]uint32, nFAT*idsPerSector)
	for i := range fat {
		fat[i] = free
	}
	for i := 0; i < nFAT; i++ {
		fat[i] = fatSect
	}
	fat[dirAt], fat[miniFATAt] = end, end
	chain := func(at uint32, n int) {
		for i := 0; i < n; i++ {
			fat[int(at)+i] = at + uint32(i) + 1
		}
		fat[int(at)+n-1] = end
	}
	chain(miniAt, nMini)
	chain(wdAt, nWD)

	miniFAT := make([]uint32, idsPerSector)
	for i := range miniFAT {
		miniFAT[i] = free
	}
	nm := len(mini) / miniSectorSize
	for i := 0; i < nm; i++ {
		miniFAT[i] = uint32(i + 1)
	}
	miniFAT[nm-1] = end

	dir := make([]byte, ss)
	entry := func(i int, name string, typ byte, child, right, start uint32, size int) {
		e := dir[i*dirEntrySize : (i+1)*dirEntrySize]
		u := utf16.Encode([]rune(name))
		for k, c := range u {
			le.PutUint16(e[2*k:], c)
		}
		le.PutUint16(e[0x40:], uint16(2*len(u)+2))
		e[0x42], e[0x43] = typ, 1
		le.PutUint32(e[0x44:], none)
		le.PutUint32(e[0x48:], right)
		le.PutUint32(e[0x4C:], child)
		le.PutUint32(e[0x74:], start)
		le.PutUint32(e[0x78:], uint32(size))
	}
	entry(0, "Root Entry", typeRoot, 1, none, miniAt, len(mini))
	entry(1, "WordDocument", typeStream, none, 2, wdAt, len(wd))
	entry(2, "1Table", typeStream, none, none, 0, len(table))

	out := make([]byte, ss*(total+1))
	h := out[:ss]
	copy(h, cfbSignature)
	le.PutUint16(h[0x18:], minorVersion)
	le.PutUint16(h[0x1A:], majorVersion)
	le.PutUint16(h[0x1C:], byteOrderMark)
	le.PutUint16(h[0x1E:], sectorShift)
	le.PutUint16(h[0x20:], miniShift)
	le.PutUint32(h[0x2C:], uint32(nFAT))
	le.PutUint32(h[0x30:], dirAt)
	le.PutUint32(h[0x38:], miniCutoff)
	le.PutUint32(h[0x3C:], miniFATAt)
	le.PutUint32(h[0x40:], 1)
	le.PutUint32(h[0x44:], end)
	for i := 0; i < headerDIFATLen; i++ {
		v := uint32(free)
		if i < nFAT {
			v = uint32(i)
		}
		le.PutUint32(h[headerDIFAT+sectorIDSize*i:], v)
	}
	at := func(s uint32) []byte { return out[(int(s)+1)*ss:] }
	for i, v := range fat {
		le.PutUint32(at(uint32(i / idsPerSector))[sectorIDSize*(i%idsPerSector):], v)
	}
	copy(at(dirAt), dir)
	for i, v := range miniFAT {
		le.PutUint32(at(miniFATAt)[sectorIDSize*i:], v)
	}
	copy(at(miniAt), mini)
	copy(at(wdAt), wd)
	return out
}
