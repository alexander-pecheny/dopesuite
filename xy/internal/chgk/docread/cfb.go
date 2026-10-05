package docread

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf16"
)

// A Compound File Binary ([MS-CFB]) is a FAT file system inside one file: the
// .doc keeps its text in the "WordDocument" stream and the piece table in
// "0Table" or "1Table". This reads the top-level streams by name and nothing
// else (no writing, no storages below the root, no property sets).

const cfbSignature = "\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1"

const (
	headerSize      = 512
	shift512        = 9  // sector size of a version 3 file
	shift4096       = 12 // sector size of a version 4 file
	miniShift64     = 6  // the only mini sector size there is
	headerDIFATLen  = 109
	headerDIFAT     = 0x4C // offset of the DIFAT array in the header
	sectorIDSize    = 4
	maxRegSect      = 0xFFFFFFFA // the highest real sector number
	maxEntryNameLen = 64         // bytes, UTF-16 with the terminator
	endOfChain      = 0xFFFFFFFE
	noStream        = 0xFFFFFFFF
	typeStream      = 2
	typeRoot        = 5
	dirEntrySz      = 128
	maxDirCount     = 1 << 20
)

// ErrNotCFB is a file that is not an OLE compound file, so not a Word 97+ .doc.
var ErrNotCFB = errors.New("not a Word .doc (no OLE compound file header)")

type cfb struct {
	data       []byte
	sectorSize int
	miniSize   int
	miniCutoff uint32
	fat        []uint32
	miniFAT    []uint32
	ministream []byte
	streams    map[string]streamRef
}

type streamRef struct {
	start uint32
	size  int64
}

// openCFB indexes the root storage's streams; stream reads one of them.
// Nothing here allocates more than the file's own size: every count and chain
// read from the file is bounded by the sectors the file actually has, since a
// crafted header must not exhaust the memory of whoever reads it.
func openCFB(data []byte) (*cfb, error) {
	if len(data) < headerSize || string(data[:len(cfbSignature)]) != cfbSignature {
		return nil, ErrNotCFB
	}
	le := binary.LittleEndian
	shift := le.Uint16(data[0x1E:])
	miniShift := le.Uint16(data[0x20:])
	if shift != shift512 && shift != shift4096 || miniShift != miniShift64 {
		return nil, fmt.Errorf("cfb: unsupported sector size 2^%d", shift)
	}
	c := &cfb{
		data:       data,
		sectorSize: 1 << shift,
		miniSize:   1 << miniShift,
		miniCutoff: le.Uint32(data[0x38:]),
		streams:    map[string]streamRef{},
	}

	// The FAT's own sectors are listed by the DIFAT: 109 entries in the header,
	// the rest in a chain of DIFAT sectors whose last entry links the next one.
	numFAT := int(le.Uint32(data[0x2C:]))
	if sectors := len(data)/c.sectorSize - 1; numFAT > sectors {
		return nil, fmt.Errorf("cfb: more FAT sectors than the file has")
	}
	var fatSectors []uint32
	for i := 0; i < headerDIFATLen && len(fatSectors) < numFAT; i++ {
		fatSectors = append(fatSectors, le.Uint32(data[headerDIFAT+sectorIDSize*i:]))
	}
	per := c.sectorSize/sectorIDSize - 1
	next := le.Uint32(data[0x44:])
	for seen := 0; len(fatSectors) < numFAT && next <= maxRegSect; seen++ {
		sec, err := c.sector(next)
		if err != nil || seen > numFAT {
			return nil, fmt.Errorf("cfb: broken DIFAT chain")
		}
		for i := 0; i < per && len(fatSectors) < numFAT; i++ {
			fatSectors = append(fatSectors, le.Uint32(sec[sectorIDSize*i:]))
		}
		next = le.Uint32(sec[sectorIDSize*per:])
	}
	for _, s := range fatSectors {
		sec, err := c.sector(s)
		if err != nil {
			return nil, fmt.Errorf("cfb: FAT sector: %w", err)
		}
		for i := 0; i < c.sectorSize; i += sectorIDSize {
			c.fat = append(c.fat, le.Uint32(sec[i:]))
		}
	}

	dir, err := c.chain(le.Uint32(data[0x30:]), -1)
	if err != nil {
		return nil, fmt.Errorf("cfb: directory: %w", err)
	}
	n := len(dir) / dirEntrySz
	if n == 0 || n > maxDirCount {
		return nil, fmt.Errorf("cfb: empty directory")
	}
	entry := func(i uint32) []byte { return dir[int(i)*dirEntrySz : int(i+1)*dirEntrySz] }
	root := entry(0)
	if root[0x42] != typeRoot {
		return nil, fmt.Errorf("cfb: no root entry")
	}

	if mf := le.Uint32(data[0x3C:]); mf <= maxRegSect {
		raw, err := c.chain(mf, -1)
		if err != nil {
			return nil, fmt.Errorf("cfb: mini FAT: %w", err)
		}
		for i := 0; i+sectorIDSize <= len(raw); i += sectorIDSize {
			c.miniFAT = append(c.miniFAT, le.Uint32(raw[i:]))
		}
		c.ministream, err = c.chain(le.Uint32(root[0x74:]), int64(le.Uint32(root[0x78:])))
		if err != nil {
			return nil, fmt.Errorf("cfb: mini stream: %w", err)
		}
	}

	// The root's children are a red-black tree over the left/right sibling
	// links. Only that level is indexed: an embedded document (ObjectPool) is
	// a storage further down with its own "WordDocument" stream.
	visited := map[uint32]bool{}
	for stack := []uint32{le.Uint32(root[0x4C:])}; len(stack) > 0; {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if i == noStream {
			continue
		}
		if int(i) >= n || visited[i] {
			return nil, fmt.Errorf("cfb: broken directory tree")
		}
		visited[i] = true
		e := entry(i)
		stack = append(stack, le.Uint32(e[0x44:]), le.Uint32(e[0x48:]))
		if e[0x42] != typeStream {
			continue
		}
		nameLen := int(le.Uint16(e[0x40:]))
		if nameLen < 2 || nameLen > maxEntryNameLen {
			return nil, fmt.Errorf("cfb: bad entry name")
		}
		u := make([]uint16, nameLen/2-1)
		for k := range u {
			u[k] = le.Uint16(e[2*k:])
		}
		c.streams[string(utf16.Decode(u))] = streamRef{start: le.Uint32(e[0x74:]), size: int64(le.Uint32(e[0x78:]))}
	}
	return c, nil
}

// stream reads a top-level stream; ok is false when there is none by that name.
func (c *cfb) stream(name string) (body []byte, ok bool, err error) {
	ref, ok := c.streams[name]
	if !ok {
		return nil, false, nil
	}
	if uint32(ref.size) < c.miniCutoff {
		body, err = c.miniChain(ref.start, ref.size)
	} else {
		body, err = c.chain(ref.start, ref.size)
	}
	if err != nil {
		return nil, true, fmt.Errorf("cfb: stream %q: %w", name, err)
	}
	return body, true, nil
}

func (c *cfb) sector(i uint32) ([]byte, error) {
	off := (int64(i) + 1) * int64(c.sectorSize)
	if off+int64(c.sectorSize) > int64(len(c.data)) {
		return nil, fmt.Errorf("sector %d past the end of the file", i)
	}
	return c.data[off : off+int64(c.sectorSize)], nil
}

// chain reads a FAT chain; size < 0 keeps every sector of it. A chain longer
// than the file has sectors loops, so it is refused before it outgrows the file.
func (c *cfb) chain(start uint32, size int64) ([]byte, error) {
	if size > int64(len(c.data)) {
		return nil, fmt.Errorf("stream larger than the file")
	}
	var out []byte
	maxSteps := len(c.data) / c.sectorSize
	for s, steps := start, 0; s != endOfChain; steps++ {
		if int(s) >= len(c.fat) || steps >= maxSteps {
			return nil, fmt.Errorf("broken sector chain")
		}
		sec, err := c.sector(s)
		if err != nil {
			return nil, err
		}
		out = append(out, sec...)
		if size >= 0 && int64(len(out)) >= size {
			break
		}
		s = c.fat[s]
	}
	if size >= 0 {
		if int64(len(out)) < size {
			return nil, fmt.Errorf("stream shorter than its size")
		}
		out = out[:size]
	}
	return out, nil
}

func (c *cfb) miniChain(start uint32, size int64) ([]byte, error) {
	var out []byte
	for s, steps := start, 0; int64(len(out)) < size; steps++ {
		if int(s) >= len(c.miniFAT) || steps > len(c.miniFAT) {
			return nil, fmt.Errorf("broken mini sector chain")
		}
		off := int(s) * c.miniSize
		if off+c.miniSize > len(c.ministream) {
			return nil, fmt.Errorf("mini sector past the mini stream")
		}
		out = append(out, c.ministream[off:off+c.miniSize]...)
		s = c.miniFAT[s]
	}
	return out[:size], nil
}
