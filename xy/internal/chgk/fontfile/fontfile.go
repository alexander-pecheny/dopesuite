// Package fontfile reads the two things the exporters need out of a font file: its
// table directory, and the family name a .docx has to spell in its styles.
package fontfile

import (
	"encoding/binary"
	"os"
	"unicode/utf16"
)

// The sfnt table directory.
const (
	offsetTableLen = 12 // sfnt version, numTables and the search hints
	numTablesAt    = 4
	tableRecordLen = 16
	tagLen         = 4
	tableOffsetAt  = 8
	tableLengthAt  = 12
)

// The "name" table.
const (
	nameHeaderLen   = 6 // format, count, stringOffset
	nameStorageAt   = 4
	nameRecordLen   = 12
	nameEncodingAt  = 2
	nameIDAt        = 6
	nameLengthAt    = 8
	nameOffsetAt    = 10
	nameIDFamily    = 1
	nameIDTypoFam   = 16
	platformUnicode = 0
	platformMac     = 1
	platformWindows = 3
)

// Tables indexes a font file's table directory. A collection is not handled:
// the faces this looks for are single fonts.
func Tables(data []byte) map[string][]byte {
	out := map[string][]byte{}
	if len(data) < offsetTableLen {
		return out
	}
	count := int(Be16(data[numTablesAt:]))
	for i := range count {
		rec := offsetTableLen + i*tableRecordLen
		if rec+tableRecordLen > len(data) {
			break
		}
		offset, length := int(be32(data[rec+tableOffsetAt:])), int(be32(data[rec+tableLengthAt:]))
		if offset < 0 || length < 0 || offset+length > len(data) {
			continue
		}
		out[string(data[rec:rec+tagLen])] = data[offset : offset+length]
	}
	return out
}

// Family reads the font file's family name, which is what a .docx and a .pptx
// must ask Word and PowerPoint for: the typographic family (nameID 16) when the
// file names one, and the legacy family (nameID 1) otherwise. That is
// chgksuite's order, and it matters for a face like NotoSans-Bold, whose legacy
// family is the weight and whose typographic family is not.
func Family(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return FamilyOf(data), nil
}

// FamilyOf is Family for bytes already in hand.
func FamilyOf(data []byte) string {
	name := Tables(data)["name"]
	if len(name) < nameHeaderLen {
		return ""
	}
	count, storage := int(Be16(name[2:])), int(Be16(name[nameStorageAt:]))
	byID := map[int]string{}
	for i := range count {
		rec := nameHeaderLen + i*nameRecordLen
		if rec+nameRecordLen > len(name) {
			break
		}
		platform, nameID, s := readNameRecord(name, rec, storage)
		if nameID != nameIDFamily && nameID != nameIDTypoFam || s == "" {
			continue
		}
		if _, seen := byID[nameID]; !seen || platform == platformWindows {
			byID[nameID] = s
		}
	}
	if s := byID[nameIDTypoFam]; s != "" {
		return s
	}
	return byID[nameIDFamily]
}

// readNameRecord decodes the name record at rec; s is empty when the string
// lies outside the table.
func readNameRecord(name []byte, rec, storage int) (platform, nameID int, s string) {
	platform, encoding := int(Be16(name[rec:])), int(Be16(name[rec+nameEncodingAt:]))
	nameID = int(Be16(name[rec+nameIDAt:]))
	length, offset := int(Be16(name[rec+nameLengthAt:])), int(Be16(name[rec+nameOffsetAt:]))
	from := storage + offset
	if from < 0 || from+length > len(name) {
		return platform, nameID, ""
	}
	raw := name[from : from+length]
	// Windows and the modern Mac tables are UTF-16BE; the old Mac Roman one
	// is bytes.
	if platform == platformWindows || platform == platformUnicode || (platform == platformMac && encoding != 0) {
		return platform, nameID, decodeUTF16BE(raw)
	}
	return platform, nameID, string(raw)
}

func decodeUTF16BE(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, Be16(b[i:]))
	}
	return string(utf16.Decode(u))
}

// Be16 reads a big-endian uint16, which is how every field of a font is stored.
func Be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }
func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
