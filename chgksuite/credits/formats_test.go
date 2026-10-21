package credits

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

const textPacket = "Кубок чего-то\n\nРедакторы благодарят за тестирование Петра Петрова.\n\nТур 1\n\n" +
	"Вопрос 1. Секрет?\nОтвет: секрет.\n\nВопрос 2. Секрет?\nОтвет: секрет.\n"

func wantPacket(t *testing.T, c *Credits, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if c.Questions != 2 || len(c.Paragraphs) == 0 || !strings.Contains(c.Paragraphs[0].Text, "Петра Петрова") {
		t.Fatalf("got %+v", c)
	}
}

// A .txt packet in a Windows code page and an .rtf one read like any other.
func TestTextAndRTF(t *testing.T) {
	cp1251, err := charmap.Windows1251.NewEncoder().String(textPacket)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Read("пакет.txt", []byte(cp1251), Options{})
	wantPacket(t, c, err)

	var rtf strings.Builder
	rtf.WriteString(`{\rtf1\ansi\ansicpg1251{\fonttbl{\f0\fcharset204 Times;}}{\info{\title Секрет}}\f0 `)
	for _, line := range strings.Split(textPacket, "\n") {
		for _, r := range line {
			rtf.WriteString(`\u` + strconv.Itoa(int(r)) + `?`)
		}
		rtf.WriteString(`\par `)
	}
	rtf.WriteString("}")
	c, err = Read("пакет.rtf", []byte(rtf.String()), Options{})
	wantPacket(t, c, err)
}

// A .7z gives its packet; one under a password is ErrEncrypted, not a failure
// of the reader.
func TestSevenZip(t *testing.T) {
	data, err := os.ReadFile("testdata/packet.7z")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Read("p.7z", data, Options{})
	wantPacket(t, c, err)

	locked, err := os.ReadFile("testdata/locked.7z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read("p.7z", locked, Options{}); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("locked 7z: %v", err)
	}
}

// An archive of handouts and notes is not a packet: 0 questions, no error.
// One whose documents are all under a password is ErrEncrypted.
func TestArchiveWithoutPacket(t *testing.T) {
	c, err := Read("раздатки.zip", zipOf(t, map[string]string{"01.jpg": "jpeg", "Оплата.txt": "Оплатите взнос до пятницы."}), Options{})
	if err != nil || c.Questions != 0 {
		t.Fatalf("handouts: %v %+v", err, c)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "пакет.docx", Flags: 0x1})
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("encrypted bytes"))
	zw.Close()
	if _, err := Read("p.zip", buf.Bytes(), Options{}); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("encrypted zip member: %v", err)
	}
}

// Word saves a .docx under a password as a Compound File holding an
// EncryptedPackage stream.
func TestEncryptedDocx(t *testing.T) {
	data := append(append([]byte{}, oleSignature...), make([]byte, 64)...)
	for _, r := range "EncryptedPackage" {
		data = append(data, byte(r), 0)
	}
	if _, err := Read("p.docx", data, Options{}); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("got %v", err)
	}
}

func TestUzbekCyrillic(t *testing.T) {
	uz := strings.Repeat("Муҳаррир: Ўзбекистон қишлоқ хўжалиги ғалаба ҳақида. ", 20)
	if got := language(uz); got != "uz_cyr" {
		t.Errorf("Uzbek: %q", got)
	}
	by := strings.Repeat("Рэдактар дзякуе ўсім, хто дапамагаў з пытаннямі. ", 20)
	if got := language(by); got != "by" {
		t.Errorf("Belarusian: %q", got)
	}
	kz := strings.Repeat("Редактор қазақ тілінде сұрақтар жазды, рахмет бәріне. ", 20)
	if got := language(kz); got == "uz_cyr" {
		t.Errorf("Kazakh read as Uzbek")
	}
}
