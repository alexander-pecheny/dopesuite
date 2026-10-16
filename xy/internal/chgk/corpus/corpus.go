// Package corpus reads chgksuite's test corpus for the parity tests: the
// packages in ~/chgksuite/chgksuite/tests (or $XY_CHGKSUITE_TESTS) and the
// .canon files chgksuite writes from them.
//
// Some packages are stored XOR-encrypted with the SHA-256 of a password kept
// in tests_password.txt next to them, which is not in git. Read decrypts them,
// and a test that needs one is skipped when the password is absent.
package corpus

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const encryptedExt = ".encrypted"

// Dir is the corpus directory; the test is skipped when it does not exist.
func Dir(t testing.TB) string {
	t.Helper()
	dir := os.Getenv("XY_CHGKSUITE_TESTS")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "chgksuite", "chgksuite", "tests")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("chgksuite tests dir not found (%s)", dir)
	}
	return dir
}

// Name is a fixture's file name without the ".encrypted" suffix, the name
// chgksuite parses it under.
func Name(file string) string { return strings.TrimSuffix(file, encryptedExt) }

// Read returns a fixture's bytes, decrypted if it is one of the encrypted ones
// (".encrypted", or ".encrypted.canon" for its canon).
func Read(t testing.TB, file string) []byte {
	t.Helper()
	dir := Dir(t)
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(file, encryptedExt) {
		return data
	}
	password, err := os.ReadFile(filepath.Join(dir, "tests_password.txt"))
	if err != nil {
		t.Skipf("%s is encrypted and tests_password.txt is missing", file)
	}
	key := sha256.Sum256([]byte(strings.TrimSpace(string(password))))
	for i := range data {
		data[i] ^= key[i%len(key)]
	}
	return data
}

// Canon is the fixture's .canon with Windows line ends normalised.
func Canon(t testing.TB, file string) string {
	t.Helper()
	return strings.ReplaceAll(string(Read(t, file+".canon")), "\r\n", "\n")
}

// Chgk are the packages chgksuite parses with ChgkParser (the chgk and brain
// games), plain text and .docx alike.
var Chgk = []string{
	"2019-07-23_beln19_u.docx",
	"Kubok_knyagini_Olgi-2015.docx",
	"Shkolny_Chemp_Estonii-2014_(48v).docx",
	"bkd_2021_all.txt",
	"borromeo.txt",
	"comment_first.txt",
	"haifa2025.docx.encrypted",
	"kinsbf_day_1.txt.encrypted",
	"link_unwrap.docx",
	"malkin_papkov_synchr.docx",
	"octo2021_khmelkov.docx",
	"ovsch_boronenko_3.docx",
	"pass1.docx",
	"single_number_line_test.docx",
	"source_numbering_bug.docx",
	"test_amper.docx",
	"test_blitz.txt",
	"tourrev_with_razmin.docx",
	"СЧР_Шередега_Ермишкин.docx",
}
