package htmlshot

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestChromePlatform(t *testing.T) {
	// Whatever this machine is, either it is one Chrome for Testing publishes
	// or the message says what to do instead.
	got, err := chromePlatform()
	if err != nil {
		if !strings.Contains(err.Error(), "--browser") {
			t.Errorf("the error should say how to proceed: %v", err)
		}
		return
	}
	known := map[string]bool{"linux64": true, "mac-arm64": true, "mac-x64": true, "win64": true, "win32": true}
	if !known[got] {
		t.Errorf("platform = %q, which the endpoint does not publish", got)
	}
}

func TestHeadlessShellInFindsThePlatformsName(t *testing.T) {
	dir := t.TempDir()
	name := "chrome-headless-shell"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	inner := filepath.Join(dir, "chrome-headless-shell-linux64")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, name), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := headlessShellIn(dir); got != filepath.Join(inner, name) {
		t.Errorf("= %q", got)
	}
	if got := headlessShellIn(t.TempDir()); got != "" {
		t.Errorf("an empty tree should find nothing, got %q", got)
	}
}

// The archive is Google's, but it is still an archive: an entry that climbs out
// of the directory must be refused rather than written.
func TestUnzipRefusesEscapes(t *testing.T) {
	for _, name := range []string{"../escaped.txt", "a/../../escaped.txt"} {
		archive := zipWith(t, map[string]string{name: "x"})
		if err := unzip(archive, t.TempDir()); err == nil {
			t.Errorf("%q was allowed", name)
		}
	}
}

func TestUnzipKeepsTheExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix modes")
	}
	archive := zipWithMode(t, "chrome-headless-shell-linux64/chrome-headless-shell", "x", 0o755)
	dir := t.TempDir()
	if err := unzip(archive, dir); err != nil {
		t.Fatal(err)
	}
	exe := headlessShellIn(dir)
	if exe == "" {
		t.Fatal("did not find the executable")
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, not executable", info.Mode())
	}
}

// TestUnpackRealArchive unpacks the actual mac and windows downloads, which is
// the only way to know the layout this walks is theirs. Set
// CHGKSUITE_TEST_ARCHIVES to a directory holding
// chrome-headless-shell-<platform>.zip to run it.
func TestUnpackRealArchive(t *testing.T) {
	dir := os.Getenv("CHGKSUITE_TEST_ARCHIVES")
	if dir == "" {
		t.Skip("set CHGKSUITE_TEST_ARCHIVES to a directory of downloaded archives")
	}
	for _, platform := range []string{"linux64", "mac-arm64", "mac-x64", "win64", "win32"} {
		archive := filepath.Join(dir, "chrome-headless-shell-"+platform+".zip")
		if _, err := os.Stat(archive); err != nil {
			continue
		}
		t.Run(platform, func(t *testing.T) {
			into := t.TempDir()
			if err := unzip(archive, into); err != nil {
				t.Fatal(err)
			}
			name := "chrome-headless-shell"
			if strings.HasPrefix(platform, "win") {
				name += ".exe"
			}
			found := ""
			_ = filepath.WalkDir(into, func(path string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && d.Name() == name {
					found = path
				}
				return nil
			})
			if found == "" {
				t.Fatalf("no %s in the unpacked %s archive", name, platform)
			}
			info, err := os.Stat(found)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() == 0 {
				t.Error("the executable is empty")
			}
			if !strings.HasPrefix(platform, "win") && info.Mode().Perm()&0o111 == 0 {
				t.Errorf("%s: mode %v, not executable", platform, info.Mode())
			}
		})
	}
}

func zipWith(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, buf.Bytes())
}

func zipWithMode(t *testing.T, name, body string, mode os.FileMode) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
	hdr.SetMode(mode)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, buf.Bytes())
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestInstallReusesWhatIsAlreadyThere: the second call must not download again.
func TestInstallReusesTheCache(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads a browser")
	}
	if _, err := chromePlatform(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	t.Setenv("CHGKSUITE_BROWSER_DIR", dir)
	first, err := Install(context.Background(), nil)
	if err != nil {
		t.Skipf("no network: %v", err)
	}
	second, err := Install(context.Background(), func(string) {
		t.Error("the second install downloaded again")
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("%q then %q", first, second)
	}
}
