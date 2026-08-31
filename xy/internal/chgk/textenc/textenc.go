// Package textenc reads a .txt question package whose encoding nobody recorded.
//
// chgksuite hands the bytes to chardet and refuses the file when it is less
// than 70% sure. Go has no chardet, and the packages that reach these tools are
// Russian text in one of four encodings, so this guesses among those instead:
// a file that decodes as UTF-8 is UTF-8, and otherwise the winner is the
// single-byte encoding that yields the most Russian-looking text. The reader
// can always say which with --encoding.
package textenc

import (
	"errors"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	xunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// ErrUnknown is a file whose encoding nothing here recognises.
var ErrUnknown = errors.New("encoding cannot be determined; pass it with --encoding")

// candidates are the single-byte encodings Russian question packages come in.
var candidates = []struct {
	name string
	enc  encoding.Encoding
}{
	{"cp1251", charmap.Windows1251},
	{"koi8-r", charmap.KOI8R},
	{"cp866", charmap.CodePage866},
	{"iso8859-5", charmap.ISO8859_5},
}

// Decode reads the bytes as text. name is an encoding to force ("" to guess).
// Line endings are normalized, including the doubled ones a Windows editor
// leaves behind (read_text_file).
func Decode(raw []byte, name string) (string, error) {
	raw = fixLineEndings(raw)
	if name != "" {
		return decodeAs(raw, name)
	}
	if utf8.Valid(raw) {
		return normalize(string(raw)), nil
	}
	if s, ok := decodeUTF16(raw); ok {
		return normalize(s), nil
	}
	best, bestScore := "", 0.0
	for _, c := range candidates {
		s, err := decodeWith(raw, c.enc)
		if err != nil {
			continue
		}
		if score := russianScore(s); score > bestScore {
			best, bestScore = s, score
		}
	}
	// The right encoding scores 0.87 and up, the wrong ones 0.62 and down
	// (measured across all four, and on the KOI8-R package in chgksuite's
	// corpus). Below 0.7 this is not a guess worth making: a wrong encoding
	// silently turns a package into mojibake.
	if bestScore < 0.7 {
		return "", ErrUnknown
	}
	return normalize(best), nil
}

// Encodings are the names Decode accepts, for a CLI's help.
func Encodings() []string {
	names := []string{"utf-8", "utf-16"}
	for _, c := range candidates {
		names = append(names, c.name)
	}
	return names
}

func decodeAs(raw []byte, name string) (string, error) {
	switch n := strings.ToLower(strings.ReplaceAll(name, "_", "-")); n {
	case "utf-8", "utf8":
		return normalize(string(raw)), nil
	case "utf-16", "utf16":
		s, ok := decodeUTF16(raw)
		if !ok {
			return "", ErrUnknown
		}
		return normalize(s), nil
	default:
		for _, c := range candidates {
			if c.name != n && !strings.EqualFold(strings.ReplaceAll(c.name, "-", ""), strings.ReplaceAll(n, "-", "")) {
				continue
			}
			s, err := decodeWith(raw, c.enc)
			if err != nil {
				return "", err
			}
			return normalize(s), nil
		}
	}
	return "", ErrUnknown
}

func decodeWith(raw []byte, e encoding.Encoding) (string, error) {
	out, _, err := transform.Bytes(e.NewDecoder(), raw)
	return string(out), err
}

func decodeUTF16(raw []byte) (string, bool) {
	if len(raw) < 2 {
		return "", false
	}
	bom := xunicode.UTF16(xunicode.LittleEndian, xunicode.UseBOM)
	if (raw[0] == 0xFF && raw[1] == 0xFE) || (raw[0] == 0xFE && raw[1] == 0xFF) {
		out, _, err := transform.Bytes(bom.NewDecoder(), raw)
		return string(out), err == nil
	}
	return "", false
}

// russianFrequencies is how often each letter occurs in Russian prose. The
// numbers are the usual published ones; only their shape matters here.
var russianFrequencies = map[rune]float64{
	'о': 10.98, 'е': 8.45, 'а': 8.01, 'и': 7.35, 'н': 6.70, 'т': 6.26,
	'с': 5.47, 'р': 4.73, 'в': 4.54, 'л': 4.40, 'к': 3.49, 'м': 3.21,
	'д': 2.98, 'п': 2.81, 'у': 2.62, 'я': 2.01, 'ы': 1.90, 'ь': 1.74,
	'г': 1.70, 'з': 1.65, 'б': 1.59, 'ч': 1.44, 'й': 1.21, 'х': 0.97,
	'ж': 0.94, 'ш': 0.73, 'ю': 0.64, 'ц': 0.48, 'щ': 0.36, 'э': 0.32,
	'ф': 0.26, 'ъ': 0.04, 'ё': 0.04,
}

// russianScore says how much a decoding looks like Russian: how much of it is
// Cyrillic at all, times how closely its letters are distributed like Russian
// ones. The second half is what tells the single-byte encodings apart —
// mojibake is Cyrillic too, and often lower case throughout, but it spells the
// wrong letters.
func russianScore(s string) float64 {
	counts := map[rune]float64{}
	var letters, cyrillic float64
	for _, r := range strings.ToLower(s) {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if unicode.Is(unicode.Cyrillic, r) {
			cyrillic++
			counts[r]++
		}
	}
	if letters == 0 || cyrillic == 0 {
		return 0
	}
	return cyrillic / letters * cosine(counts, cyrillic)
}

// cosine compares the observed letter distribution with Russian's.
func cosine(counts map[rune]float64, total float64) float64 {
	var dot, normObserved, normExpected float64
	for r, expected := range russianFrequencies {
		observed := counts[r] / total * 100
		dot += observed * expected
		normObserved += observed * observed
		normExpected += expected * expected
	}
	// Letters outside the alphabet (the box-drawing a wrong decoding tends to
	// produce is not one, but the other alphabets' extra letters are) still
	// count against the observed norm.
	for r, n := range counts {
		if _, known := russianFrequencies[r]; !known {
			observed := n / total * 100
			normObserved += observed * observed
		}
	}
	if normObserved == 0 || normExpected == 0 {
		return 0
	}
	return dot / (math.Sqrt(normObserved) * math.Sqrt(normExpected))
}

// fixLineEndings repairs the "\r\r\n" a round trip through a Windows editor
// leaves, before anything tries to decode it.
func fixLineEndings(raw []byte) []byte {
	if !strings.Contains(string(raw), "\r\r\n") {
		return raw
	}
	return []byte(strings.ReplaceAll(string(raw), "\r\r\n", "\n"))
}

func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}
