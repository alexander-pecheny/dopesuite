package htmlshot

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
)

// Fetching a browser, for the machine that has none. chgksuite's html2img runs
// `playwright install chromium`, which wants a Python and a Playwright; this
// takes the same artifact — Google's Chrome for Testing — straight from the
// endpoint that publishes it.
//
// What it fetches is chrome-headless-shell and not the full browser: it is the
// half that renders (--screenshot, --print-to-pdf, --dump-dom all work), and it
// is some 50 MB smaller.

// VersionsURL publishes the current build of each channel and where to get it.
const VersionsURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

// CacheDir is where a downloaded browser lives, and the first place FindBrowser
// looks after the ones the user already has.
func CacheDir() string {
	if dir := os.Getenv("CHGKSUITE_BROWSER_DIR"); dir != "" {
		return dir
	}
	if base, err := os.UserCacheDir(); err == nil {
		return filepath.Join(base, "chgksuite", "chrome")
	}
	return filepath.Join(os.TempDir(), "chgksuite-chrome")
}

// Install downloads a headless Chromium into the cache and returns the
// executable. progress, if set, is told what is happening — it is a hundred
// megabytes, and a silent minute looks like a hang.
func Install(ctx context.Context, progress func(string)) (string, error) {
	s := xystrings.Default
	platform, err := chromePlatform()
	if err != nil {
		return "", err
	}
	say := func(format string, a ...any) {
		if progress != nil {
			progress(fmt.Sprintf(format, a...))
		}
	}

	version, url, err := latestHeadlessShell(ctx, platform)
	if err != nil {
		return "", err
	}
	root := filepath.Join(CacheDir(), version)
	if exe := headlessShellIn(root); exe != "" {
		return exe, nil
	}
	say("%s", s.Install.Browser.Downloading(version, platform))
	archive, err := download(ctx, url)
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)

	// Unpack beside the target and rename, so an interrupted download never
	// leaves a half-unpacked browser that the next run would try to use.
	staging, err := os.MkdirTemp(CacheDir(), "unpack-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err := unzip(archive, staging); err != nil {
		return "", err
	}
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	if err := os.Rename(staging, root); err != nil {
		return "", err
	}
	exe := headlessShellIn(root)
	if exe == "" {
		return "", corei18n.User(s.Install.Browser.ArchiveNoShell())
	}
	say("%s", s.Install.Installed(exe))
	return exe, nil
}

func latestHeadlessShell(ctx context.Context, platform string) (version, url string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, VersionsURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", corei18n.User(xystrings.Default.Install.Browser.ReleasesFailed(resp.Status))
	}
	var body struct {
		Channels map[string]struct {
			Version   string `json:"version"`
			Downloads map[string][]struct {
				Platform string `json:"platform"`
				URL      string `json:"url"`
			} `json:"downloads"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", err
	}
	stable, ok := body.Channels["Stable"]
	if !ok {
		return "", "", corei18n.User(xystrings.Default.Install.Browser.ReleasesNoStable())
	}
	for _, d := range stable.Downloads["chrome-headless-shell"] {
		if d.Platform == platform {
			return stable.Version, d.URL, nil
		}
	}
	return "", "", corei18n.User(xystrings.Default.Install.Browser.PlatformMissing(platform))
}

// chromePlatform is the name Chrome for Testing publishes this machine under.
func chromePlatform() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "linux64", nil
	case "darwin/arm64":
		return "mac-arm64", nil
	case "darwin/amd64":
		return "mac-x64", nil
	case "windows/amd64":
		return "win64", nil
	case "windows/386":
		return "win32", nil
	}
	// Chrome for Testing publishes x86-64 and Apple silicon only; a Linux ARM
	// machine has to use its distribution's chromium.
	return "", corei18n.User(xystrings.Default.Install.Browser.NoBuild(runtime.GOOS + "/" + runtime.GOARCH))
}

func download(ctx context.Context, url string) (string, error) {
	if err := os.MkdirAll(CacheDir(), 0o755); err != nil {
		return "", err
	}
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
	f, err := os.CreateTemp(CacheDir(), "chrome-*.zip")
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

// unzip unpacks into dir, refusing any entry that would escape it.
func unzip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target := filepath.Join(dir, filepath.FromSlash(f.Name)) //nolint:gosec // checked below
		if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) {
			return corei18n.User(xystrings.Default.Install.ArchiveEscape(f.Name))
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
		// A macOS archive can carry symlinks (a framework's Versions/Current).
		// Written as a regular file, one is a file holding a path — so link it,
		// and refuse a link that points out of the directory.
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			if err := writeSymlink(f, dir, target); err != nil {
				return err
			}
			continue
		}
		if err := writeEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

func writeEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	// The archive is Google's own and its entries are bounded; the limit is
	// belt and braces against a zip bomb, at a size no browser exceeds.
	_, err = io.Copy(out, io.LimitReader(rc, 2<<30))
	return err
}

func writeSymlink(f *zip.File, dir, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, 4096))
	if err != nil {
		return err
	}
	dest := string(raw)
	resolved := dest
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(target), dest)
	}
	if !strings.HasPrefix(filepath.Clean(resolved), filepath.Clean(dir)+string(os.PathSeparator)) {
		return corei18n.User(xystrings.Default.Install.Browser.SymlinkEscape(f.Name, dest))
	}
	return os.Symlink(dest, target)
}

// headlessShellIn finds the executable inside an unpacked archive, whatever the
// platform called the directory.
func headlessShellIn(root string) string {
	name := "chrome-headless-shell"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var found string
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
