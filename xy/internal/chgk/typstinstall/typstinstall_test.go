package typstinstall

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

// The asset names typst published for v0.15.1. If a build ever stops being
// published under the name assetPrefix expects, this is where it shows.
var release = []string{
	"typst-aarch64-apple-darwin.tar.xz",
	"typst-aarch64-pc-windows-msvc.zip",
	"typst-aarch64-unknown-linux-musl.tar.xz",
	"typst-armv7-unknown-linux-musleabi.tar.xz",
	"typst-documentation.pdf",
	"typst-riscv64gc-unknown-linux-gnu.tar.xz",
	"typst-x86_64-apple-darwin.tar.xz",
	"typst-x86_64-pc-windows-msvc.zip",
	"typst-x86_64-unknown-linux-musl.tar.xz",
}

func TestAssetPrefixMatchesARealRelease(t *testing.T) {
	prefix, err := assetPrefix()
	if err != nil {
		t.Skipf("%s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
	}
	for _, name := range release {
		if strings.HasPrefix(name, prefix) && isArchive(name) {
			return
		}
	}
	t.Errorf("nothing in the release matches %q — the naming changed", prefix)
}

// Every platform this CLI is built for must find its own build, which is not
// something the machine running the test can check for itself.
func TestEveryPlatformHasABuild(t *testing.T) {
	// The pairs are what assetPrefix would produce; keep them beside the real
	// asset names above.
	want := map[string]string{
		"darwin/arm64":  "typst-aarch64-apple-darwin",
		"darwin/amd64":  "typst-x86_64-apple-darwin",
		"windows/amd64": "typst-x86_64-pc-windows-msvc",
		"windows/arm64": "typst-aarch64-pc-windows-msvc",
		"linux/amd64":   "typst-x86_64-unknown-linux-musl",
		"linux/arm64":   "typst-aarch64-unknown-linux-musl",
		"linux/arm":     "typst-armv7-unknown-linux-musleabi",
	}
	for platform, prefix := range want {
		found := false
		for _, name := range release {
			if strings.HasPrefix(name, prefix) && isArchive(name) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s wants %q, which the release does not publish", platform, prefix)
		}
	}
}

func TestExtractTarXZ(t *testing.T) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, f := range []struct {
		name string
		mode int64
		body string
	}{
		{"typst-x86_64-unknown-linux-musl/README.md", 0o644, "readme"},
		{"typst-x86_64-unknown-linux-musl/typst", 0o755, "binary"},
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	var compressed bytes.Buffer
	xw, err := xz.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := xw.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := xw.Close(); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "typst.tar.xz")
	if err := os.WriteFile(archive, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := extract(archive, dir); err != nil {
		t.Fatal(err)
	}
	found := findBinary(dir)
	if found == "" {
		t.Fatal("the binary was not found in the unpacked archive")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(found)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("mode = %v, not executable", info.Mode())
		}
	}
}

func TestSafeJoinRefusesEscapes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../out", "a/../../out"} {
		if _, err := safeJoin(dir, name); err == nil {
			t.Errorf("%q was allowed", name)
		}
	}
	// An absolute name is not an escape: Join drops the leading separator, so
	// the entry lands inside the staging directory like any other.
	got, err := safeJoin(dir, "/etc/passwd")
	if err != nil || got != filepath.Join(dir, "etc", "passwd") {
		t.Errorf("absolute entry → %q, %v", got, err)
	}
	if _, err := safeJoin(dir, "typst-x86_64/typst"); err != nil {
		t.Errorf("an ordinary entry was refused: %v", err)
	}
}
