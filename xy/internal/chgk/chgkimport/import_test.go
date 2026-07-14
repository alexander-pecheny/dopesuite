package chgkimport

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chgksuiteTests locates chgksuite's own test corpus; the canon files there are
// the output its `parse` command produces and the parity target for the whole
// import pipeline.
func chgksuiteTests(t *testing.T) string {
	dir := os.Getenv("XY_CHGKSUITE_TESTS")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "chgksuite", "chgksuite", "tests")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("chgksuite tests dir not found (%s)", dir)
	}
	return dir
}

// docxFixtures are every chgk-game .docx in chgksuite's corpus. The si/troika
// ones are excluded (different parser, out of scope for xy), as is
// octo2021_khmelkov.docx, whose canon is produced with --preserve_formatting —
// a flag the import path deliberately doesn't expose. (docxread's own test does
// cover that flag.)
var docxFixtures = []string{
	"2019-07-23_beln19_u.docx",
	"Kubok_knyagini_Olgi-2015.docx",
	"Shkolny_Chemp_Estonii-2014_(48v).docx",
	"link_unwrap.docx",
	"malkin_papkov_synchr.docx",
	"ovsch_boronenko_3.docx",
	"pass1.docx",
	"single_number_line_test.docx",
	"source_numbering_bug.docx",
	"test_amper.docx",
	"tourrev_with_razmin.docx",
	"СЧР_Шередега_Ермишкин.docx",
}

// TestDocxCanonParity runs the whole pipeline the import endpoint runs — raw
// .docx bytes in, 4s source out — and requires byte-equality with chgksuite.
func TestDocxCanonParity(t *testing.T) {
	dir := chgksuiteTests(t)
	for _, name := range docxFixtures {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(dir, name+".canon"))
			if err != nil {
				t.Fatal(err)
			}
			res, err := Parse(name, data)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got, w := res.Source, strings.ReplaceAll(string(want), "\r\n", "\n"); got != w {
				t.Errorf("source mismatch:\n%s", firstDiff(w, got))
			}

			// After sanitising, every (img …) the source references must resolve
			// to an extracted image — otherwise the import silently drops a
			// handout. This is checked with the same rule the frontend uses.
			res.SafeImageNames()
			refs := imageRefs(res.Source)
			for _, ref := range refs {
				if !hasImage(res.Images, ref) {
					t.Errorf("source references %q but it was not extracted", ref)
				}
			}
			if len(refs) != len(res.Images) {
				t.Errorf("%d (img …) refs but %d images extracted", len(refs), len(res.Images))
			}
		})
	}
}

// TestSafeImageNamesFixesUnreadableDirectives pins the parenthesis case: the
// fixture's own filename has parens, so chgksuite emits a name that every reader
// of the (img …) syntax truncates at the first ')'.
func TestSafeImageNamesFixesUnreadableDirectives(t *testing.T) {
	dir := chgksuiteTests(t)
	name := "Shkolny_Chemp_Estonii-2014_(48v).docx"
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(name, data)
	if err != nil {
		t.Fatal(err)
	}
	if got := imageRefs(res.Source); len(got) == 0 || hasImage(res.Images, got[0]) {
		t.Fatalf("expected the raw name to be truncated by the (img …) rule, got %q", got)
	}
	res.SafeImageNames()
	for _, ref := range imageRefs(res.Source) {
		if !hasImage(res.Images, ref) {
			t.Errorf("after SafeImageNames, %q still does not resolve", ref)
		}
	}
	if strings.ContainsAny(res.Images[0].Name, "() ") {
		t.Errorf("image name %q still has directive-breaking characters", res.Images[0].Name)
	}
}

// TestZipRoundTrip covers the .zip path: one .4s plus its images.
func TestZipRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, body []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	add("pack/questions.4s", []byte("### Тест\r\n\r\n? Вопрос (img pic.jpg)\r\n! Ответ\r\n"))
	add("pack/pic.jpg", []byte{0xff, 0xd8, 0xff})
	add("__MACOSX/._pic.jpg", []byte("junk"))
	add("pack/.DS_Store", []byte("junk"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	res, err := Parse("pack.zip", buf.Bytes())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.Name != "questions" {
		t.Errorf("name = %q, want %q (from the .4s inside)", res.Name, "questions")
	}
	if strings.Contains(res.Source, "\r") {
		t.Error("CRLF survived into the source")
	}
	if len(res.Images) != 1 || res.Images[0].Name != "pic.jpg" || res.Images[0].MIME != "image/jpeg" {
		t.Fatalf("images = %+v, want just pic.jpg (archiver junk filtered)", res.Images)
	}
}

func TestUnsupported(t *testing.T) {
	if _, err := Parse("notes.pdf", []byte("x")); err == nil {
		t.Fatal("want an error for .pdf")
	}
}

func hasImage(imgs []Image, name string) bool {
	for _, i := range imgs {
		if i.Name == name {
			return true
		}
	}
	return false
}

// imageRefs pulls the filename out of each (img …) directive; like chgksuite's
// parseimg, the name is the last whitespace-separated token.
func imageRefs(source string) []string {
	var out []string
	for _, part := range strings.Split(source, "(img")[1:] {
		end := strings.IndexByte(part, ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(part[:end])
		if len(fields) > 0 {
			out = append(out, fields[len(fields)-1])
		}
	}
	return out
}

func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return "line " + itoa(i+1) + "\n- want: " + wl + "\n+ got:  " + gl
		}
	}
	return "(only trailing differences)"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestRejectsNon4sText: a .4s that holds no questions must fail at import rather
// than quietly become an empty list.
func TestRejectsNon4sText(t *testing.T) {
	if _, err := Parse("notes.4s", []byte("просто заметки\nбез единого маркера\n")); err == nil {
		t.Fatal("want an error for a .4s with no 4s markers")
	}
	if _, err := Parse("ok.4s", []byte("? Вопрос\n! Ответ\n")); err != nil {
		t.Fatalf("a minimal valid .4s must import: %v", err)
	}
}

// zipOf builds an archive from name→body pairs.
func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestZipRelativeImageRefs: a package that refers to its handouts by archive path
// must still resolve, since images are stored under their base name.
func TestZipRelativeImageRefs(t *testing.T) {
	res, err := Parse("p.zip", zipOf(t, map[string][]byte{
		"q.4s":          []byte("? Вопрос (img images/q3.png)\n! Ответ\n"),
		"images/q3.png": {1, 2, 3},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := imageRefs(res.Source); len(got) != 1 || got[0] != "q3.png" {
		t.Fatalf("directive should be rewritten to the stored name, got %q", got)
	}
	if !hasImage(res.Images, "q3.png") {
		t.Fatal("image not extracted under its base name")
	}
}

// TestZipDuplicateBasenames: two images with the same base name are ambiguous to
// an (img …) directive, so the first must win rather than the directive being
// silently repointed at the second.
func TestZipDuplicateBasenames(t *testing.T) {
	res, err := Parse("p.zip", zipOf(t, map[string][]byte{
		"q.4s":      []byte("? Вопрос (img pic.png)\n! Ответ\n"),
		"a/pic.png": {0xaa},
		"b/pic.png": {0xbb},
	}))
	if err != nil {
		t.Fatal(err)
	}
	res.SafeImageNames()
	if len(res.Images) != 1 {
		t.Fatalf("want a single pic.png, got %d images: %+v", len(res.Images), res.Images)
	}
	if got := imageRefs(res.Source); len(got) != 1 || got[0] != "pic.png" {
		t.Fatalf("the directive must be left alone, got %q", got)
	}
}

// TestSafeImageNamesLeavesProseAlone: the rename must touch (img …) directives
// only, not the same text where it appears in the question.
func TestSafeImageNamesLeavesProseAlone(t *testing.T) {
	r := &Result{
		Source: "? Файл a(1).png упомянут в тексте. (img a(1).png)\n! Ответ\n",
		Images: []Image{{Name: "a(1).png"}},
	}
	r.SafeImageNames()
	if !strings.Contains(r.Source, "Файл a(1).png упомянут") {
		t.Errorf("prose was rewritten: %q", r.Source)
	}
	if !strings.Contains(r.Source, "(img a_1_.png)") {
		t.Errorf("directive was not rewritten: %q", r.Source)
	}
}
