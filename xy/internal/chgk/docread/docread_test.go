package docread

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"xy/internal/chgk/docread/doctest"
)

// TestPieces: the text comes out in piece-table order whatever order the
// pieces sit in the file, compressed pieces read as cp1252, and nothing past
// the main document (footnotes, headers) is read.
func TestPieces(t *testing.T) {
	data := doctest.Pieces([]doctest.Piece{
		{Text: "Тур 1\rРедактор — Иван Иванов\r"},
		{Text: "It’s cp1252\r", Compressed: true},
		{Text: "Сноска, которой нет в тексте\r"},
	}, doctest.Options{Main: len([]rune("Тур 1\rРедактор — Иван Иванов\rIt’s cp1252\r"))})
	got, err := ToText(data)
	if err != nil {
		t.Fatal(err)
	}
	want := "Тур 1\n\nРедактор — Иван Иванов\n\nIt’s cp1252"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestControlCharacters: Word's marks become docxread's plain text.
func TestControlCharacters(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"field keeps its result", "См. \x13 HYPERLINK \"http://x\" \x14сайт\x15.\r", "См. сайт."},
		{"nested field", "\x13 IF \x13 PAGE \x141\x15 = 1 \"да\" \x14да\x15\r", "да"},
		{"field without a result", "Дата:\x13 DATE \x15\r", "Дата:"},
		{"table cells", "Тур\x07Редактор\x07\x07\r", "Тур\n\nРедактор"},
		{"line break", "Тестировали:\x0bАнна, Пётр\r", "Тестировали:\nАнна, Пётр"},
		{"hyphens", "сине\x1eзелёный пере\x1fнос\r", "сине-зелёный перенос"},
		{"anchors", "Вопрос\x01\x08 1\x02.\r", "Вопрос 1."},
		{"underscore is escaped", "Ответ: _\r", "Ответ: \\_"},
		{"page break", "Тур 1\x0cТур 2\r", "Тур 1\n\nТур 2"},
	} {
		got, err := ToText(doctest.Doc(c.in))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if strings.TrimSpace(got) != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestRefusals: what the reader does not read is an error naming it, and a
// broken or hostile file is an error too, never a panic or a huge allocation.
func TestRefusals(t *testing.T) {
	if _, err := ToText(doctest.Pieces([]doctest.Piece{{Text: "x\r"}}, doctest.Options{Encrypted: true})); !errors.Is(err, ErrEncrypted) {
		t.Errorf("encrypted: %v", err)
	}
	if _, err := ToText(doctest.Pieces([]doctest.Piece{{Text: "x\r"}}, doctest.Options{Ident: 0xA5DC})); !errors.Is(err, ErrOldWord) {
		t.Errorf("Word 95: %v", err)
	}
	if _, err := ToText([]byte("{\\rtf1 not a doc}")); !errors.Is(err, ErrNotCFB) {
		t.Errorf("rtf: %v", err)
	}
	// A header claiming more FAT sectors than the file holds, or text longer
	// than the document, is refused before anything that size is allocated.
	data := doctest.Doc("Тур 1\r")
	huge := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(huge[0x2C:], 0x10000000)
	if _, err := ToText(huge); err == nil {
		t.Error("a FAT larger than the file was read")
	}
	long := doctest.Pieces([]doctest.Piece{{Text: "x\r"}}, doctest.Options{Main: 1 << 30})
	if _, err := ToText(long); err == nil {
		t.Error("text longer than the document was read")
	}
	// Truncated anywhere, a file is an error, never a panic.
	for n := 0; n < len(data); n += 97 {
		_, _ = ToText(data[:n])
	}
}
