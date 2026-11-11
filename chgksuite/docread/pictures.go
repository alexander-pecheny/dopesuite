package docread

import (
	"encoding/binary"
	"strconv"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/docxread"
)

// Pictures of a .doc ([MS-DOC] 2.9.192 PICFAndOfficeArtData, [MS-ODRAW] for
// the OfficeArt records). A picture stands in the text as an anchor character:
//
//   - 0x01, an inline picture: its character run carries sprmCPicLocation, the
//     offset of a PICF in the Data stream, after which come the picture's shape
//     and an OfficeArtFBSE holding its BLIP;
//   - 0x08, a floating shape: PlcfSpaMom gives the shape id at that CP, the
//     drawing (OfficeArtContent at fcDggInfo) the shape's pib property, an index
//     into the BStore, whose FBSE holds the BLIP or points into WordDocument.
//
// Only PNG, JPEG and DIB (written out as BMP) are kept; a metafile (WMF, EMF,
// PICT) or a TIFF has no browser to draw it.

// FIB pairs and sprms.
const (
	bteChpxPair      = 12 // fcPlcfBteChpx/lcbPlcfBteChpx
	spaMomPair       = 40 // fcPlcSpaMom
	dggInfoPair      = 50 // fcDggInfo: OfficeArtContent
	fkpSize          = 512
	pnMask           = 0x3FFFFF
	sprmCPicLocation = 0x6A03
	spaSize          = 26
	inlinePicture    = 0x01
	floatingShape    = 0x08
	mmShapeFile      = 0x0066 // the PICF is followed by the picture's name
	picfHeaderMin    = 8
)

// OfficeArt record types.
const (
	recHeaderSize = 8
	recContainer  = 0xF
	rtDggContain  = 0xF000
	rtBStore      = 0xF001
	rtSpContainer = 0xF004
	rtFBSE        = 0xF007
	rtFSP         = 0xF00A
	rtFOPT        = 0xF00B
	rtBlipJPEG    = 0xF01D
	rtBlipPNG     = 0xF01E
	rtBlipDIB     = 0xF01F
	rtBlipJPEGCMY = 0xF02A
	fbseFixed     = 36
	fbseFoDelay   = 28
	fbseCbName    = 33
	propSize      = 6
	propPib       = 0x0104
	propIDMask    = 0x3FFF
	uidSize       = 16
	blipTagSize   = 1
	bmpHeaderSize = 14
	maxPictures   = 200
)

// Image is a picture of the document, named as its (img …) directive names it.
type Image struct {
	Name string
	Data []byte
}

// The marker render writes for a picture, swapped for its directive at the end.
const (
	pictureMark    = ""
	pictureMarkEnd = ""
)

// ToTextImages is ToText with the document's pictures: each becomes
// "(img <prefix>imageN.ext)" where its anchor stands, and an Image of that
// name. A picture that can't be read, or that a browser can't draw, is left
// out as ToText leaves out every picture.
func ToTextImages(data []byte, prefix string) (string, []Image, error) {
	d, err := open(data)
	if err != nil {
		return "", nil, err
	}
	marks, images := d.pictures(prefix)
	text := docxread.Tidy(render(d.raw, marks))
	for i, img := range images {
		text = strings.Replace(text, pictureMark+strconv.Itoa(i)+pictureMarkEnd, "(img "+img.Name+")", 1)
	}
	return text, images, nil
}

// pictures reads every anchor's picture: by CP, the number of its image (the
// marker's key), and the images.
func (d *document) pictures(prefix string) (map[int]string, []Image) {
	marks := map[int]string{}
	var images []Image
	var runs []chpxRun
	var shapes map[uint32]uint32
	var bstore []record
	var store bool
	dataStream, _, _ := d.c.stream("Data")
	for cp, r := range d.raw {
		if len(images) == maxPictures || r != inlinePicture && r != floatingShape {
			continue
		}
		var ext string
		var body []byte
		if r == inlinePicture {
			if runs == nil {
				runs = d.chpxRuns()
			}
			if loc, ok := picLocation(runs, d.fcs[cp]); ok {
				ext, body = inlineBlip(dataStream, loc)
			}
		} else {
			if !store {
				shapes, bstore = d.drawing()
				store = true
			}
			if spid, ok := d.shapeAt(cp); ok {
				if pib, ok := shapes[spid]; ok && pib >= 1 && int(pib) <= len(bstore) {
					ext, body = fbseBlip(bstore[pib-1], d.wd)
				}
			}
		}
		if ext == "" || len(body) == 0 {
			continue
		}
		name := prefix + "image" + strconv.Itoa(len(images)+1) + "." + ext
		marks[cp] = strconv.Itoa(len(images))
		images = append(images, Image{Name: name, Data: body})
	}
	return marks, images
}

// chpxRun is a run of characters, by stream offset, with its character
// properties.
type chpxRun struct {
	start, end uint32
	grpprl     []byte
}

// chpxRuns reads the character runs of the CHPX FKPs PlcBteChpx points to.
func (d *document) chpxRuns() []chpxRun {
	runs := []chpxRun{}
	plc, ok := d.tableBlock(bteChpxPair)
	if !ok || len(plc) < cpSize {
		return runs
	}
	le := binary.LittleEndian
	n := (len(plc) - cpSize) / (cpSize + cpSize)
	for i := range n {
		pn := le.Uint32(plc[cpSize*(n+1)+cpSize*i:]) & pnMask
		off := uint64(pn) * fkpSize
		if off+fkpSize > uint64(len(d.wd)) {
			continue
		}
		page := d.wd[off : off+fkpSize]
		crun := int(page[fkpSize-1])
		if (crun+1)*cpSize+crun > fkpSize-1 {
			continue
		}
		for k := range crun {
			start, end := le.Uint32(page[cpSize*k:]), le.Uint32(page[cpSize*(k+1):])
			at := 2 * int(page[(crun+1)*cpSize+k])
			var grpprl []byte
			if at > 0 && at < fkpSize-1 {
				cb := int(page[at])
				if at+1+cb <= fkpSize-1 {
					grpprl = page[at+1 : at+1+cb]
				}
			}
			runs = append(runs, chpxRun{start: start, end: end, grpprl: grpprl})
		}
	}
	return runs
}

// picLocation is the sprmCPicLocation of the run holding the character at fc.
func picLocation(runs []chpxRun, fc uint32) (uint32, bool) {
	for _, run := range runs {
		if fc >= run.start && fc < run.end {
			return sprmOperand(run.grpprl, sprmCPicLocation)
		}
	}
	return 0, false
}

// sprmOperand finds a 4-byte sprm's operand in a grpprl ([MS-DOC] 2.6.1: the
// operand's size is in the sprm's spra bits).
func sprmOperand(grpprl []byte, want uint16) (uint32, bool) {
	le := binary.LittleEndian
	for p := 0; p+2 <= len(grpprl); {
		sprm := le.Uint16(grpprl[p:])
		p += 2
		var size int
		switch sprm >> 13 {
		case 0, 1:
			size = 1
		case 2, 4, 5:
			size = 2
		case 3:
			size = 4
		case 7:
			size = 3
		default: // 6: the first byte is the size
			if p >= len(grpprl) {
				return 0, false
			}
			size = 1 + int(grpprl[p])
		}
		if p+size > len(grpprl) {
			return 0, false
		}
		if sprm == want && size == 4 {
			return le.Uint32(grpprl[p:]), true
		}
		p += size
	}
	return 0, false
}

// inlineBlip reads the picture of a PICF at loc in the Data stream: the BLIP
// of the first FBSE after the picture's shape container.
func inlineBlip(data []byte, loc uint32) (string, []byte) {
	le := binary.LittleEndian
	if uint64(loc)+picfHeaderMin > uint64(len(data)) {
		return "", nil
	}
	lcb := uint64(le.Uint32(data[loc:]))
	end := min(uint64(len(data)), uint64(loc)+lcb)
	pos := uint64(loc) + uint64(le.Uint16(data[loc+4:]))
	if le.Uint16(data[loc+6:]) == mmShapeFile && pos < end {
		pos += 1 + uint64(data[pos])
	}
	if pos >= end {
		return "", nil
	}
	for _, rec := range records(data[pos:end]) {
		switch {
		case rec.typ == rtFBSE:
			if ext, body := fbseBlip(rec, nil); ext != "" {
				return ext, body
			}
		case isBlip(rec.typ):
			if ext, body := blip(rec); ext != "" {
				return ext, body
			}
		}
	}
	return "", nil
}

// shapeAt is the id of the floating shape anchored at cp (PlcfSpaMom).
func (d *document) shapeAt(cp int) (uint32, bool) {
	plc, ok := d.tableBlock(spaMomPair)
	if !ok || len(plc) < cpSize {
		return 0, false
	}
	n := (len(plc) - cpSize) / (cpSize + spaSize)
	le := binary.LittleEndian
	for i := range n {
		if int(le.Uint32(plc[cpSize*i:])) == cp {
			return le.Uint32(plc[cpSize*(n+1)+spaSize*i:]), true
		}
	}
	return 0, false
}

// drawing reads OfficeArtContent: each shape's pib by its id, and the BStore's
// FBSEs in order (pib 1 is the first).
func (d *document) drawing() (map[uint32]uint32, []record) {
	shapes := map[uint32]uint32{}
	var bstore []record
	content, ok := d.tableBlock(dggInfoPair)
	if !ok {
		return shapes, nil
	}
	recs := records(content)
	// After the DggContainer: drawings, each a dgglbl byte and a DgContainer.
	if len(recs) > 0 && recs[0].typ == rtDggContain {
		for _, c := range records(recs[0].body) {
			if c.typ == rtBStore {
				bstore = records(c.body)
			}
		}
		rest := content[recs[0].size:]
		for len(rest) > 1 {
			drawings := records(rest[1:])
			if len(drawings) == 0 {
				break
			}
			collectShapes(drawings[:1], shapes)
			rest = rest[1+drawings[0].size:]
		}
	}
	return shapes, bstore
}

// collectShapes walks containers for shapes: an FSP's id with its FOPT's pib.
func collectShapes(recs []record, shapes map[uint32]uint32) {
	le := binary.LittleEndian
	for _, rec := range recs {
		if rec.typ == rtSpContainer {
			var spid, pib uint32
			var hasID, hasPib bool
			for _, c := range records(rec.body) {
				switch c.typ {
				case rtFSP:
					if len(c.body) >= 4 {
						spid, hasID = le.Uint32(c.body), true
					}
				case rtFOPT:
					for i := 0; i < int(c.instance) && (i+1)*propSize <= len(c.body); i++ {
						if le.Uint16(c.body[i*propSize:])&propIDMask == propPib {
							pib, hasPib = le.Uint32(c.body[i*propSize+2:]), true
						}
					}
				}
			}
			if hasID && hasPib {
				shapes[spid] = pib
			}
		}
		if rec.ver == recContainer {
			collectShapes(records(rec.body), shapes)
		}
	}
}

// fbseBlip is the BLIP an FBSE holds, or the one at its foDelay in delay (the
// WordDocument stream) when it holds none.
func fbseBlip(fbse record, delay []byte) (string, []byte) {
	b := fbse.body
	if len(b) < fbseFixed {
		return "", nil
	}
	embedded := fbseFixed + int(b[fbseCbName])
	if embedded < len(b) {
		if recs := records(b[embedded:]); len(recs) > 0 && isBlip(recs[0].typ) {
			return blip(recs[0])
		}
		return "", nil
	}
	off := binary.LittleEndian.Uint32(b[fbseFoDelay:])
	if delay == nil || uint64(off) >= uint64(len(delay)) {
		return "", nil
	}
	if recs := records(delay[off:]); len(recs) > 0 && isBlip(recs[0].typ) {
		return blip(recs[0])
	}
	return "", nil
}

func isBlip(typ uint16) bool {
	return typ >= 0xF018 && typ <= 0xF117
}

// blip gives a BLIP's picture: after one or two 16-byte uids (the instance
// says which) and a tag byte. A DIB gets the file header that makes it a BMP.
func blip(rec record) (string, []byte) {
	var ext string
	uids := 1
	switch rec.typ {
	case rtBlipPNG:
		ext, uids = "png", map[uint16]int{0x6E0: 1, 0x6E1: 2}[rec.instance]
	case rtBlipJPEG, rtBlipJPEGCMY:
		ext, uids = "jpg", map[uint16]int{0x46A: 1, 0x6E2: 1, 0x46B: 2, 0x6E3: 2}[rec.instance]
	case rtBlipDIB:
		ext, uids = "bmp", map[uint16]int{0x7A8: 1, 0x7A9: 2}[rec.instance]
	default:
		return "", nil
	}
	start := uids*uidSize + blipTagSize
	if uids == 0 || start >= len(rec.body) {
		return "", nil
	}
	body := rec.body[start:]
	if ext == "bmp" {
		return dibToBMP(body)
	}
	return ext, body
}

// dibToBMP puts the 14-byte file header before a DIB (a BITMAPINFOHEADER, its
// palette and the pixels).
func dibToBMP(dib []byte) (string, []byte) {
	le := binary.LittleEndian
	if len(dib) < 40 {
		return "", nil
	}
	header := int(le.Uint32(dib))
	bitCount := int(le.Uint16(dib[14:]))
	compression := le.Uint32(dib[16:])
	colors := int(le.Uint32(dib[32:]))
	if colors == 0 && bitCount <= 8 {
		colors = 1 << bitCount
	}
	masks := 0
	if compression == 3 && header == 40 { // BI_BITFIELDS: three masks after the header
		masks = 12
	}
	offBits := bmpHeaderSize + header + masks + 4*colors
	if header < 40 || offBits > bmpHeaderSize+len(dib) {
		return "", nil
	}
	out := make([]byte, bmpHeaderSize, bmpHeaderSize+len(dib))
	out[0], out[1] = 'B', 'M'
	le.PutUint32(out[2:], uint32(bmpHeaderSize+len(dib)))
	le.PutUint32(out[10:], uint32(offBits))
	return "bmp", append(out, dib...)
}

// record is one OfficeArt record: its header's version, instance and type,
// its body and its whole size.
type record struct {
	ver      uint8
	instance uint16
	typ      uint16
	body     []byte
	size     int
}

// records splits b into the records it starts with; a record running past the
// end ends the list.
func records(b []byte) []record {
	le := binary.LittleEndian
	var out []record
	for len(b) >= recHeaderSize {
		verInst := le.Uint16(b)
		n := uint64(le.Uint32(b[4:]))
		if recHeaderSize+n > uint64(len(b)) {
			break
		}
		size := recHeaderSize + int(n)
		out = append(out, record{ver: uint8(verInst & 0xF), instance: verInst >> 4, typ: le.Uint16(b[2:]), body: b[recHeaderSize:size], size: size})
		b = b[size:]
	}
	return out
}
