// Package typstinstall finds the typst binary, and fetches one when the machine
// has none — the way chgksuite's handouter/installer.py does, and into the same
// directory, so the two tools share whichever of them installed it.
package typstinstall

import (
	"archive/tar"
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ulikunitz/xz"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
)

// ReleasesURL is where typst publishes its builds.
const ReleasesURL = "https://api.github.com/repos/typst/typst/releases/latest"

// UtilsDir is ~/.pecheny_utils, which is chgksuite's own get_utils_dir. Sharing
// it means a machine that has run either tool has a typst for both.
func UtilsDir() (string, error) {
	if dir := os.Getenv("CHGKSUITE_UTILS_DIR"); dir != "" {
		return dir, os.MkdirAll(dir, 0o755)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".pecheny_utils")
	return dir, os.MkdirAll(dir, 0o755)
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "typst.exe"
	}
	return "typst"
}

// Find is get_typst_path: the typst on PATH, else the one in the utils dir.
// Both are checked by running them, as chgksuite checks.
func Find() string {
	if path, err := exec.LookPath(binaryName()); err == nil && works(path) {
		return path
	}
	dir, err := UtilsDir()
	if err != nil {
		return ""
	}
	if path := filepath.Join(dir, binaryName()); works(path) {
		return path
	}
	return ""
}

func works(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "--version").Run() == nil
}

// Install downloads the latest typst release into the utils dir and returns the
// binary. progress, if set, is told what is happening.
func Install(ctx context.Context, progress func(string)) (string, error) {
	s := xystrings.Default
	say := func(format string, a ...any) {
		if progress != nil {
			progress(fmt.Sprintf(format, a...))
		}
	}
	version, url, err := latestAsset(ctx)
	if err != nil {
		return "", err
	}
	dir, err := UtilsDir()
	if err != nil {
		return "", err
	}
	say("%s", s.Install.Typst.Downloading(version))
	archive, err := download(ctx, url, dir)
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)

	staging, err := os.MkdirTemp(dir, "typst-unpack-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err := extract(archive, staging); err != nil {
		return "", err
	}
	// The binary sits inside a per-target directory in the archive.
	found := findBinary(staging)
	if found == "" {
		return "", corei18n.User(s.Install.Typst.ArchiveNoBinary(binaryName()))
	}
	target := filepath.Join(dir, binaryName())
	if err := move(found, target); err != nil {
		return "", err
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return "", err
	}
	say("%s", s.Install.Installed(target))
	return target, nil
}

// FindOrInstall is Find, fetching one when there is nothing to find.
func FindOrInstall(ctx context.Context, progress func(string)) (string, error) {
	if path := Find(); path != "" {
		return path, nil
	}
	if progress != nil {
		progress(xystrings.Default.Install.Typst.NotFound())
	}
	return Install(ctx, progress)
}

// latestAsset picks the build for this machine out of the latest release, by
// the same triple chgksuite matches on: musl on Linux, msvc on Windows, and
// whatever Apple calls the architecture on a Mac.
func latestAsset(ctx context.Context) (version, url string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ReleasesURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", corei18n.User(xystrings.Default.Install.Typst.ReleasesFailed(resp.Status))
	}
	var body struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", err
	}
	want, err := assetPrefix()
	if err != nil {
		return "", "", err
	}
	for _, a := range body.Assets {
		if strings.HasPrefix(a.Name, want) && isArchive(a.Name) {
			return body.Tag, a.URL, nil
		}
	}
	return "", "", corei18n.User(xystrings.Default.Install.Typst.ReleaseNoBuild(want))
}

// assetPrefix is the "typst-<arch>-<target>" a release asset is named by.
func assetPrefix() (string, error) {
	arch := ""
	switch runtime.GOARCH {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	case "arm":
		arch = "armv7"
	case "riscv64":
		arch = "riscv64gc"
	default:
		return "", corei18n.User(xystrings.Default.Install.Typst.PlatformMissing(runtime.GOARCH))
	}
	switch runtime.GOOS {
	case "darwin":
		return "typst-" + arch + "-apple-darwin", nil
	case "windows":
		return "typst-" + arch + "-pc-windows-msvc", nil
	case "linux":
		if arch == "riscv64gc" {
			return "typst-" + arch + "-unknown-linux-gnu", nil
		}
		if arch == "armv7" {
			return "typst-" + arch + "-unknown-linux-musleabi", nil
		}
		return "typst-" + arch + "-unknown-linux-musl", nil
	}
	return "", corei18n.User(xystrings.Default.Install.Typst.PlatformMissing(runtime.GOOS))
}

func isArchive(name string) bool {
	return strings.HasSuffix(name, ".tar.xz") || strings.HasSuffix(name, ".tar.gz") ||
		strings.HasSuffix(name, ".zip")
}

func download(ctx context.Context, url, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	f, err := os.CreateTemp(dir, "typst-*"+archiveExt(url))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func archiveExt(url string) string {
	for _, ext := range []string{".tar.xz", ".tar.gz", ".zip"} {
		if strings.HasSuffix(url, ext) {
			return ext
		}
	}
	return ""
}

func extract(archive, dir string) error {
	switch {
	case strings.HasSuffix(archive, ".zip"):
		return extractZip(archive, dir)
	case strings.HasSuffix(archive, ".tar.xz"), strings.HasSuffix(archive, ".tar.gz"):
		return extractTar(archive, dir)
	}
	return corei18n.User(xystrings.Default.Install.Typst.UnknownArchive(archive))
}

// extractTar unpacks the .tar.xz every platform but Windows publishes. Go has
// no xz in the standard library, hence the one dependency here.
func extractTar(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(archive, ".xz") {
		if r, err = xz.NewReader(f); err != nil {
			return err
		}
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeFile(tr, target, os.FileMode(hdr.Mode).Perm()); err != nil {
				return err
			}
		}
	}
}

func extractZip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, err := safeJoin(dir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(rc, target, f.Mode().Perm())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// safeJoin refuses an entry that would write outside the directory.
func safeJoin(dir, name string) (string, error) {
	target := filepath.Join(dir, filepath.FromSlash(name)) //nolint:gosec // checked here
	if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) {
		return "", corei18n.User(xystrings.Default.Install.ArchiveEscape(name))
	}
	return target, nil
}

func writeFile(r io.Reader, target string, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	// Bounded well above any typst build, against an archive that lies.
	_, err = io.Copy(out, io.LimitReader(r, 1<<30))
	return err
}

func findBinary(root string) string {
	name := binaryName()
	found := ""
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" || d.IsDir() {
			return nil //nolint:nilerr // an unreadable entry is simply not it
		}
		if d.Name() == name {
			found = path
		}
		return nil
	})
	return found
}

// move is a rename, falling back to a copy when the staging directory and the
// target turn out to be on different filesystems.
func move(from, to string) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	return writeFile(src, to, 0o755)
}
