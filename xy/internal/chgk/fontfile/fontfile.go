// Package fontfile reads the two things the exporters need out of a font file: its
// table directory, and the family name a .docx has to spell in its styles.
package fontfile

import (
	"os"
	"unicode/utf16"
)

// Tables indexes a font file's table directory. A collection is not handled:
// the faces this looks for are single fonts.
func Tables(data []byte) map[string][]byte {
	out := map[string][]byte{}
	if len(data) < 12 {
		return out
	}
	count := int(Be16(data[4:]))
	for i := range count {
		rec := 12 + i*16
		if rec+16 > len(data) {
			break
		}
		offset, length := int(be32(data[rec+8:])), int(be32(data[rec+12:]))
		if offset < 0 || length < 0 || offset+length > len(data) {
			continue
		}
		out[string(data[rec:rec+4])] = data[offset : offset+length]
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
	if len(name) < 6 {
		return ""
	}
	count, storage := int(Be16(name[2:])), int(Be16(name[4:]))
	byID := map[int]string{}
	for i := range count {
		rec := 6 + i*12
		if rec+12 > len(name) {
			break
		}
		platform, encoding, nameID := int(Be16(name[rec:])), int(Be16(name[rec+2:])), int(Be16(name[rec+6:]))
		if nameID != 1 && nameID != 16 {
			continue
		}
		length, offset := int(Be16(name[rec+8:])), int(Be16(name[rec+10:]))
		from := storage + offset
		if from < 0 || from+length > len(name) {
			continue
		}
		raw := name[from : from+length]
		// Windows and the modern Mac tables are UTF-16BE; the old Mac Roman one
		// is bytes.
		var s string
		if platform == 3 || platform == 0 || (platform == 1 && encoding != 0) {
			s = decodeUTF16BE(raw)
		} else {
			s = string(raw)
		}
		if s == "" {
			continue
		}
		if _, seen := byID[nameID]; !seen || platform == 3 {
			byID[nameID] = s
		}
	}
	if s := byID[16]; s != "" {
		return s
	}
	return byID[1]
}

func decodeUTF16BE(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, Be16(b[i:]))
	}
	return string(utf16.Decode(u))
}

// Be16 reads a big-endian uint16, which is how every field of a font is stored.
func Be16(b []byte) uint16 { return uint16(b[0])<<8 | uint16(b[1]) }
func be32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
