package imgconv

import (
	"errors"
	"fmt"
	"sort"
)

// OptimizeJPEG rewrites a baseline JPEG with Huffman tables built for the image
// it actually holds, instead of the fixed ones Go's encoder always writes. It is
// lossless — the coefficients are untouched, only the codes that spell them —
// and it is what Pillow's `optimize=True` does, which is the whole of the
// difference between a deck this tool builds and one chgksuite builds.
//
// Anything it does not recognise it declines to touch: the input here is always
// Go's own encoder's output, so the parser is strict on purpose and the caller
// keeps the original bytes when it says no.
func OptimizeJPEG(data []byte) ([]byte, error) {
	j, err := readJPEG(data)
	if err != nil {
		return nil, err
	}
	records, err := j.decodeScan()
	if err != nil {
		return nil, err
	}
	tables := make([]*huffSpec, 8)
	for _, r := range records {
		if tables[r.table] == nil {
			tables[r.table] = &huffSpec{}
		}
		tables[r.table].freq[r.symbol]++
	}
	for _, t := range tables {
		if t != nil {
			t.build()
		}
	}
	return j.reassemble(tables, records)
}

// ── reading ─────────────────────────────────────────────────────────────────

type jpegComponent struct {
	id     byte
	h, v   int
	dcTbl  int
	acTbl  int
	blocks int // blocks per MCU
}

type jpegFile struct {
	prologue    []byte // everything up to and including SOF, minus the tables
	quant       []byte // the DQT segments, in order
	sof         []byte
	sos         []byte // the SOS header
	scan        []byte // the entropy-coded bytes, still stuffed
	comps       []jpegComponent
	mcusX       int
	mcusY       int
	dec         [8]*huffDecoder
	restarts    int
	progressive bool
}

var errUnsupportedJPEG = errors.New("not a baseline JPEG this can rewrite")

func readJPEG(data []byte) (*jpegFile, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, errUnsupportedJPEG
	}
	j := &jpegFile{}
	i := 2
	for i+3 < len(data) {
		if data[i] != 0xFF {
			return nil, errUnsupportedJPEG
		}
		marker := data[i+1]
		if marker == 0xD9 { // EOI
			break
		}
		size := int(data[i+2])<<8 | int(data[i+3])
		if size < 2 || i+2+size > len(data) {
			return nil, errUnsupportedJPEG
		}
		segment := data[i : i+2+size]
		payload := data[i+4 : i+2+size]

		switch {
		case marker == 0xC0: // SOF0, baseline
			if err := j.readSOF(payload); err != nil {
				return nil, err
			}
			j.sof = segment
		case marker == 0xC2: // SOF2, progressive
			return nil, errUnsupportedJPEG
		case marker == 0xC4: // DHT
			if err := j.readDHT(payload); err != nil {
				return nil, err
			}
		case marker == 0xDB: // DQT
			j.quant = append(j.quant, segment...)
		case marker == 0xDD: // DRI
			if len(payload) >= 2 && (int(payload[0])<<8|int(payload[1])) != 0 {
				return nil, errUnsupportedJPEG
			}
		case marker == 0xDA: // SOS
			if err := j.readSOS(payload); err != nil {
				return nil, err
			}
			j.sos = segment
			j.scan = data[i+2+size:]
			return j, nil
		default:
			// APPn, COM and the rest ride along untouched.
			j.prologue = append(j.prologue, segment...)
		}
		i += 2 + size
	}
	return nil, errUnsupportedJPEG
}

func (j *jpegFile) readSOF(p []byte) error {
	if len(p) < 6 || p[0] != 8 {
		return errUnsupportedJPEG
	}
	height := int(p[1])<<8 | int(p[2])
	width := int(p[3])<<8 | int(p[4])
	n := int(p[5])
	if n == 0 || len(p) < 6+3*n {
		return errUnsupportedJPEG
	}
	hMax, vMax := 1, 1
	for k := range n {
		c := jpegComponent{id: p[6+3*k], h: int(p[7+3*k] >> 4), v: int(p[7+3*k] & 15)}
		if c.h < 1 || c.v < 1 || c.h > 4 || c.v > 4 {
			return errUnsupportedJPEG
		}
		hMax, vMax = max(hMax, c.h), max(vMax, c.v)
		j.comps = append(j.comps, c)
	}
	for k := range j.comps {
		j.comps[k].blocks = j.comps[k].h * j.comps[k].v
	}
	j.mcusX = (width + 8*hMax - 1) / (8 * hMax)
	j.mcusY = (height + 8*vMax - 1) / (8 * vMax)
	return nil
}

func (j *jpegFile) readDHT(p []byte) error {
	for len(p) > 17 {
		class, id := int(p[0]>>4), int(p[0]&15)
		if class > 1 || id > 3 {
			return errUnsupportedJPEG
		}
		counts := p[1:17]
		total := 0
		for _, c := range counts {
			total += int(c)
		}
		if len(p) < 17+total {
			return errUnsupportedJPEG
		}
		j.dec[class*4+id] = newHuffDecoder(counts, p[17:17+total])
		p = p[17+total:]
	}
	return nil
}

func (j *jpegFile) readSOS(p []byte) error {
	if len(p) < 1 {
		return errUnsupportedJPEG
	}
	n := int(p[0])
	if n != len(j.comps) || len(p) < 1+2*n+3 {
		return errUnsupportedJPEG
	}
	for k := range n {
		id, tables := p[1+2*k], p[2+2*k]
		idx := -1
		for c := range j.comps {
			if j.comps[c].id == id {
				idx = c
			}
		}
		if idx < 0 {
			return errUnsupportedJPEG
		}
		j.comps[idx].dcTbl = int(tables >> 4)
		j.comps[idx].acTbl = int(tables & 15)
	}
	// Ss, Se, Ah/Al: a baseline scan is the whole spectrum, unshifted.
	tail := p[1+2*n:]
	if tail[0] != 0 || tail[1] != 63 || tail[2] != 0 {
		return errUnsupportedJPEG
	}
	return nil
}

// ── the entropy-coded scan ──────────────────────────────────────────────────

// record is one Huffman symbol and the raw bits that followed it. Re-spelling a
// scan means writing the same records with different codes, so the coefficients
// never have to be reconstructed.
type record struct {
	table  int
	symbol byte
	extra  uint32
	nExtra int
}

type bitReader struct {
	data []byte
	i    int
	bits uint32
	n    int
}

func (b *bitReader) readBit() (uint32, error) {
	if b.n == 0 {
		if b.i >= len(b.data) {
			return 0, errUnsupportedJPEG
		}
		c := b.data[b.i]
		b.i++
		if c == 0xFF {
			if b.i >= len(b.data) {
				return 0, errUnsupportedJPEG
			}
			// A stuffed zero is not data; any other marker ends the scan.
			if b.data[b.i] != 0x00 {
				return 0, errUnsupportedJPEG
			}
			b.i++
		}
		b.bits, b.n = uint32(c), 8
	}
	b.n--
	return (b.bits >> b.n) & 1, nil
}

func (b *bitReader) readBits(n int) (uint32, error) {
	var v uint32
	for range n {
		bit, err := b.readBit()
		if err != nil {
			return 0, err
		}
		v = v<<1 | bit
	}
	return v, nil
}

// huffDecoder is a canonical Huffman table, walked one bit at a time — the scan
// is read once, so a fast path would buy nothing.
type huffDecoder struct {
	counts  [16]int
	symbols []byte
	mincode [16]int
	maxcode [16]int
	valptr  [16]int
}

func newHuffDecoder(counts []byte, symbols []byte) *huffDecoder {
	d := &huffDecoder{symbols: append([]byte(nil), symbols...)}
	code, k := 0, 0
	for i := range 16 {
		d.counts[i] = int(counts[i])
		d.valptr[i] = k
		d.mincode[i] = code
		code += d.counts[i]
		k += d.counts[i]
		d.maxcode[i] = code - 1
		if d.counts[i] == 0 {
			d.maxcode[i] = -1
		}
		code <<= 1
	}
	return d
}

func (d *huffDecoder) decode(b *bitReader) (byte, error) {
	code := 0
	for i := range 16 {
		bit, err := b.readBit()
		if err != nil {
			return 0, err
		}
		code = code<<1 | int(bit)
		if d.maxcode[i] >= 0 && code <= d.maxcode[i] && code >= d.mincode[i] {
			return d.symbols[d.valptr[i]+code-d.mincode[i]], nil
		}
	}
	return 0, errUnsupportedJPEG
}

// decodeScan walks the whole scan, MCU by MCU, recording every symbol it reads.
func (j *jpegFile) decodeScan() ([]record, error) {
	br := &bitReader{data: j.scan}
	records := make([]record, 0, 4096)
	for range j.mcusY * j.mcusX {
		for ci := range j.comps {
			c := j.comps[ci]
			for range c.blocks {
				var err error
				records, err = j.decodeBlock(br, c, records)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return records, nil
}

func (j *jpegFile) decodeBlock(br *bitReader, c jpegComponent, out []record) ([]record, error) {
	dc, ac := j.dec[c.dcTbl], j.dec[4+c.acTbl]
	if dc == nil || ac == nil {
		return nil, errUnsupportedJPEG
	}
	symbol, err := dc.decode(br)
	if err != nil {
		return nil, err
	}
	n := int(symbol)
	if n > 16 {
		return nil, errUnsupportedJPEG
	}
	extra, err := br.readBits(n)
	if err != nil {
		return nil, err
	}
	out = append(out, record{table: c.dcTbl, symbol: symbol, extra: extra, nExtra: n})

	for k := 1; k < 64; {
		symbol, err := ac.decode(br)
		if err != nil {
			return nil, err
		}
		run, size := int(symbol>>4), int(symbol&15)
		extra, err := br.readBits(size)
		if err != nil {
			return nil, err
		}
		out = append(out, record{table: 4 + c.acTbl, symbol: symbol, extra: extra, nExtra: size})
		switch {
		case size == 0 && run != 15: // end of block
			k = 64
		default:
			k += run + 1
		}
	}
	return out, nil
}

// ── building the tables ─────────────────────────────────────────────────────

// huffSpec is one table's symbol frequencies and the canonical code they earn.
type huffSpec struct {
	freq    [257]int32
	counts  [17]int
	symbols []byte
	code    [256]uint32
	size    [256]int
}

// build is Annex K.2: merge the two least frequent symbols until one is left,
// then read each symbol's code length off the merge tree. The extra symbol at
// 256 is the spec's own reservation, which keeps the all-ones code free.
func (h *huffSpec) build() {
	freq := h.freq
	freq[256] = 1
	others := [257]int{}
	for i := range others {
		others[i] = -1
	}
	codeSize := [257]int{}

	for {
		v1 := leastFrequent(&freq, -1)
		if v1 < 0 {
			break
		}
		v2 := leastFrequent(&freq, v1)
		if v2 < 0 {
			break
		}
		freq[v1] += freq[v2]
		freq[v2] = 0
		codeSize[v1]++
		for others[v1] >= 0 {
			v1 = others[v1]
			codeSize[v1]++
		}
		others[v1] = v2
		codeSize[v2]++
		for others[v2] >= 0 {
			v2 = others[v2]
			codeSize[v2]++
		}
	}

	bits := [33]int{}
	for i := range 257 {
		if codeSize[i] > 0 {
			bits[codeSize[i]]++
		}
	}
	limitTo16(&bits)

	// The reserved symbol takes the last code of the longest length.
	for i := 32; i > 0; i-- {
		if bits[i] > 0 {
			bits[i]--
			break
		}
	}
	for i := range 17 {
		h.counts[i] = bits[i]
	}

	type sym struct {
		value byte
		size  int
	}
	var syms []sym
	for i := range 256 {
		if codeSize[i] > 0 {
			syms = append(syms, sym{byte(i), codeSize[i]})
		}
	}
	sort.SliceStable(syms, func(a, b int) bool { return syms[a].size < syms[b].size })
	for _, s := range syms {
		h.symbols = append(h.symbols, s.value)
	}

	code, k := uint32(0), 0
	for length := 1; length <= 16; length++ {
		for range h.counts[length] {
			if k >= len(syms) {
				break
			}
			value := syms[k].value
			h.code[value], h.size[value] = code, length
			code++
			k++
		}
		code <<= 1
	}
}

func leastFrequent(freq *[257]int32, exclude int) int {
	best, bestFreq := -1, int32(0)
	for i := range 257 {
		if freq[i] == 0 || i == exclude {
			continue
		}
		// The spec breaks ties towards the later symbol, which is what makes
		// two encoders agree on the same table.
		if best < 0 || freq[i] < bestFreq || (freq[i] == bestFreq && i > best) {
			best, bestFreq = i, freq[i]
		}
	}
	return best
}

// limitTo16 is the spec's own procedure for pulling codes longer than 16 bits
// back under the limit, which the merge above can produce for a skewed image.
func limitTo16(bits *[33]int) {
	for i := 32; i > 16; i-- {
		for bits[i] > 0 {
			j := i - 2
			for j > 0 && bits[j] == 0 {
				j--
			}
			if j == 0 {
				break
			}
			bits[i] -= 2
			bits[i-1]++
			bits[j+1] += 2
			bits[j]--
		}
	}
	for i := 32; i > 16; i-- {
		bits[16] += bits[i]
		bits[i] = 0
	}
	for bits[16] > 0 && bits[16] > (1<<16)-1 {
		bits[16]--
	}
}

// ── writing ─────────────────────────────────────────────────────────────────

type bitWriter struct {
	out  []byte
	bits uint32
	n    int
}

func (w *bitWriter) write(code uint32, size int) {
	for i := size - 1; i >= 0; i-- {
		w.bits = w.bits<<1 | (code>>i)&1
		w.n++
		if w.n == 8 {
			b := byte(w.bits)
			w.out = append(w.out, b)
			if b == 0xFF {
				w.out = append(w.out, 0x00)
			}
			w.bits, w.n = 0, 0
		}
	}
}

// flush pads the last byte with ones, which is what a JPEG decoder expects.
func (w *bitWriter) flush() {
	for w.n != 0 {
		w.write(1, 1)
	}
}

func (j *jpegFile) reassemble(tables []*huffSpec, records []record) ([]byte, error) {
	var w bitWriter
	w.out = make([]byte, 0, len(j.scan))
	for _, r := range records {
		t := tables[r.table]
		if t == nil || t.size[r.symbol] == 0 {
			return nil, fmt.Errorf("symbol %d has no code in table %d", r.symbol, r.table)
		}
		w.write(t.code[r.symbol], t.size[r.symbol])
		w.write(r.extra, r.nExtra)
	}
	w.flush()

	out := []byte{0xFF, 0xD8}
	out = append(out, j.prologue...)
	out = append(out, j.quant...)
	out = append(out, j.sof...)
	for i, t := range tables {
		if t == nil {
			continue
		}
		out = append(out, dhtSegment(i, t)...)
	}
	out = append(out, j.sos...)
	out = append(out, w.out...)
	return append(out, 0xFF, 0xD9), nil
}

func dhtSegment(index int, t *huffSpec) []byte {
	length := 2 + 1 + 16 + len(t.symbols)
	seg := []byte{0xFF, 0xC4, byte(length >> 8), byte(length)}
	seg = append(seg, byte(index/4<<4|index%4))
	for i := 1; i <= 16; i++ {
		seg = append(seg, byte(t.counts[i]))
	}
	return append(seg, t.symbols...)
}
