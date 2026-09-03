// Package htmlshot is chgksuite's `handouts html2img`: a handout laid out by
// hand in HTML, rendered to the PDF that goes to the printer and the PNG that
// goes into a chat.
//
// chgksuite drives Chromium through Playwright, which means a Python runtime
// and a browser it installs itself. This drives a Chromium the user already has
// (or names) through its command line, so the dependency is one program and not
// a stack: --headless takes a screenshot and prints a PDF, and the one thing it
// cannot do from the command line — measuring the laid-out content — is asked
// for by putting the answer into the DOM and reading it back with --dump-dom.
package htmlshot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
)

// Options are the switches `handouts html2img` takes.
type Options struct {
	// Browser is the Chromium to drive; empty looks for one.
	Browser string
	// Scale is --scale: the PNG's device pixel ratio.
	Scale float64
	// Timeout bounds one browser run.
	Timeout time.Duration
	// NoSandbox drops Chromium's sandbox. Off by default and turned on only
	// when the browser says it has none it can use — which is what a distro
	// that forbids unprivileged user namespaces (Ubuntu 23.10+ with AppArmor)
	// leaves it with. Say sets it, so the fallback is not silent.
	NoSandbox bool
	// Say, if set, is told about anything the run had to work around.
	Say func(string)
}

// Result is what was written and how big the page came out.
type Result struct {
	PDF, PNG          string
	WidthMM, HeightMM float64
}

var reWidthMM = regexp.MustCompile(`width:\s*([\d.]+)mm`)

// WidthMM is _parse_width_from_html: the body width the CSS declares, which is
// the viewport the page is laid out in.
func WidthMM(html string) (float64, error) {
	m := reWidthMM.FindStringSubmatch(html)
	if m == nil {
		return 0, corei18n.User(xystrings.Default.Docs.Print.WidthMissing())
	}
	return strconv.ParseFloat(m[1], 64)
}

// Render writes <name>.pdf and <name>.png beside the HTML.
func Render(ctx context.Context, path string, o Options) (Result, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Result{}, err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, err
	}
	widthMM, err := WidthMM(string(raw))
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}
	browser, err := FindOrInstall(ctx, o.Browser, o.Say)
	if err != nil {
		return Result{}, err
	}
	if o.Scale <= 0 {
		o.Scale = 1
	}
	if o.Timeout <= 0 {
		o.Timeout = time.Minute
	}
	widthPx := mmToPx(widthMM)

	scratch, err := os.MkdirTemp("", "chgksuite-html2img-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(scratch)

	heightPx, contentWidthPx, err := measure(ctx, browser, string(raw), widthPx, scratch, &o)
	if err != nil {
		return Result{}, err
	}

	base := strings.TrimSuffix(abs, filepath.Ext(abs))
	res := Result{
		PDF:     base + ".pdf",
		PNG:     base + ".png",
		WidthMM: pxToMM(contentWidthPx), HeightMM: pxToMM(heightPx),
	}

	// The PDF's page is the content's own size. Chromium takes that from the
	// document's @page rule, which is where Playwright's width/height end up
	// too, so the injected copy is rendered rather than the original.
	paged := filepath.Join(scratch, "paged.html")
	if err := os.WriteFile(paged, []byte(withPageRule(string(raw), res.WidthMM, res.HeightMM)), 0o600); err != nil {
		return Result{}, err
	}
	if err := run(ctx, browser, &o, []string{
		"--print-to-pdf=" + res.PDF, "--no-pdf-header-footer",
		fileURL(paged),
	}); err != nil {
		return Result{}, corei18n.User(xystrings.Default.Docs.Print.Pdf(err.Error()))
	}
	if err := run(ctx, browser, &o, []string{
		"--screenshot=" + res.PNG,
		fmt.Sprintf("--window-size=%d,%d", widthPx, heightPx),
		"--force-device-scale-factor=" + trimFloat(o.Scale),
		"--hide-scrollbars", "--default-background-color=ffffffff",
		fileURL(abs),
	}); err != nil {
		return Result{}, corei18n.User(xystrings.Default.Docs.Print.Png(err.Error()))
	}
	return res, nil
}

// measure lays the page out at the given width and reads back how tall it came
// out. Chromium's command line cannot return a number, so the page is asked to
// write it into an element and the DOM is dumped.
func measure(ctx context.Context, browser, html string, widthPx int, scratch string, o *Options) (height, width int, err error) {
	probe := filepath.Join(scratch, "measure.html")
	if err := os.WriteFile(probe, []byte(html+measureScript), 0o600); err != nil {
		return 0, 0, err
	}
	out, err := output(ctx, browser, o, []string{
		"--dump-dom", fmt.Sprintf("--window-size=%d,%d", widthPx, 1),
		"--virtual-time-budget=5000", fileURL(probe),
	})
	if err != nil {
		return 0, 0, corei18n.User(xystrings.Default.Docs.Print.Measure(err.Error()))
	}
	m := reMeasured.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, corei18n.User(xystrings.Default.Docs.Print.MeasureNoSize())
	}
	height, _ = strconv.Atoi(m[1])
	width, _ = strconv.Atoi(m[2])
	if height <= 0 {
		return 0, 0, corei18n.User(xystrings.Default.Docs.Print.MeasureZeroHeight())
	}
	return height, max(width, widthPx), nil
}

// measureScript is what page.evaluate does in html_handout.py, written into the
// document so --dump-dom carries the answer out.
const measureScript = `
<script id="chgksuite-measure-script">
(function () {
  var b = document.body, h = document.documentElement;
  var el = document.createElement("div");
  el.id = "chgksuite-measured";
  el.setAttribute("data-size",
    Math.max(b.scrollHeight, h.scrollHeight) + "x" + Math.max(b.scrollWidth, h.scrollWidth));
  el.style.display = "none";
  h.appendChild(el);
})();
</script>`

var reMeasured = regexp.MustCompile(`id="chgksuite-measured" data-size="(\d+)x(\d+)"`)

// withPageRule gives the document the page size the PDF is printed at, and
// tells the print engine to keep the backgrounds — which is Playwright's
// print_background=True, and has no command-line flag.
func withPageRule(html string, widthMM, heightMM float64) string {
	rule := fmt.Sprintf(
		"<style>@page { size: %.2fmm %.2fmm; margin: 0; }"+
			"* { -webkit-print-color-adjust: exact; print-color-adjust: exact; }</style>",
		widthMM, heightMM)
	if i := strings.Index(html, "</head>"); i >= 0 {
		return html[:i] + rule + html[i:]
	}
	return rule + html
}

// FindBrowser resolves the Chromium to drive, in the order a user would want:
// the one named, $CHGKSUITE_BROWSER, one this command downloaded before, one of
// the usual names on PATH, the well-known place the platform installs Chrome,
// and the copy Playwright keeps — which is what chgksuite's own html2img
// downloads, so a machine that has run it already has one.
func FindBrowser(named string) (string, error) {
	if path, ok := findBrowser(named); ok {
		return path, nil
	}
	return "", corei18n.User(xystrings.Default.Install.Browser.NotFound())
}

func findBrowser(named string) (string, bool) {
	if named != "" {
		return named, true
	}
	if env := os.Getenv("CHGKSUITE_BROWSER"); env != "" {
		return env, true
	}
	if path := installedBrowser(); path != "" {
		return path, true
	}
	for _, name := range []string{
		"chromium", "chromium-browser", "chrome-headless-shell",
		"google-chrome", "google-chrome-stable", "chrome",
		"microsoft-edge", "brave-browser",
	} {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	for _, path := range platformBrowsers() {
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	if path := playwrightChromium(); path != "" {
		return path, true
	}
	return "", false
}

// FindOrInstall is FindBrowser, downloading one when the machine has none —
// which is what chgksuite's html2img does through Playwright.
func FindOrInstall(ctx context.Context, named string, progress func(string)) (string, error) {
	if path, ok := findBrowser(named); ok {
		return path, nil
	}
	if progress != nil {
		progress(xystrings.Default.Install.Browser.NotInstalled())
	}
	return Install(ctx, progress)
}

// installedBrowser is whatever a previous run downloaded, newest first.
func installedBrowser() string {
	entries, err := os.ReadDir(CacheDir())
	if err != nil {
		return "" //nolint:nilerr // nothing downloaded yet
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		if exe := headlessShellIn(filepath.Join(CacheDir(), name)); exe != "" {
			return exe
		}
	}
	return ""
}

// platformBrowsers are the places a platform's own installer puts Chrome, which
// are not on PATH on macOS or Windows.
func platformBrowsers() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	case "windows":
		var out []string
		for _, env := range []string{"PROGRAMFILES", "PROGRAMFILES(X86)", "LOCALAPPDATA"} {
			if base := os.Getenv(env); base != "" {
				out = append(out,
					filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
					filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"))
			}
		}
		return out
	}
	return nil
}

// playwrightChromium looks where Playwright puts the browsers it downloads, so
// a machine that has run chgksuite's own html2img already has one.
func playwrightChromium() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	roots := []string{filepath.Join(home, ".cache", "ms-playwright")}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		roots = append(roots, filepath.Join(local, "ms-playwright"))
	}
	roots = append(roots, filepath.Join(home, "Library", "Caches", "ms-playwright"))
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		// The headless shell first: it is the half that renders, and it starts
		// faster than the full browser.
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "chromium_headless_shell-") {
				if exe := headlessShellIn(filepath.Join(root, e.Name())); exe != "" {
					return exe
				}
			}
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "chromium-") {
				continue
			}
			for _, rel := range []string{
				filepath.Join("chrome-linux64", "chrome"),
				filepath.Join("chrome-linux", "chrome"),
				filepath.Join("chrome-mac", "Chromium.app", "Contents", "MacOS", "Chromium"),
				filepath.Join("chrome-win", "chrome.exe"),
			} {
				path := filepath.Join(root, e.Name(), rel)
				if _, err := os.Stat(path); err == nil {
					return path
				}
			}
		}
	}
	return ""
}

// run drives one headless browser, discarding what it printed.
func run(ctx context.Context, browser string, o *Options, args []string) error {
	_, err := output(ctx, browser, o, args)
	return err
}

func output(ctx context.Context, browser string, o *Options, args []string) (string, error) {
	out, stderr, err := runOnce(ctx, browser, *o, args)
	if err == nil {
		return out, nil
	}
	// A distro that forbids unprivileged user namespaces leaves Chromium with
	// no sandbox it can use, and it refuses to start rather than drop it
	// quietly. Playwright passes --no-sandbox for the same reason.
	if !o.NoSandbox && strings.Contains(stderr, "No usable sandbox") {
		o.NoSandbox = true
		if o.Say != nil {
			o.Say(xystrings.Default.Install.Browser.NoSandbox())
		}
		out, stderr, err = runOnce(ctx, browser, *o, args)
		if err == nil {
			return out, nil
		}
	}
	if stderr != "" {
		return "", fmt.Errorf("%w: %s", err, firstLine(stderr))
	}
	return "", err
}

func runOnce(ctx context.Context, browser string, o Options, args []string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	// No --user-data-dir: headless Chromium makes its own temporary profile,
	// and given one explicitly it stays running after --dump-dom has printed.
	full := []string{
		"--headless", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
	}
	if o.NoSandbox {
		full = append(full, "--no-sandbox")
	}
	cmd := exec.CommandContext(ctx, browser, append(full, args...)...)
	out, err := cmd.Output()
	stderr := ""
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		stderr = string(ee.Stderr)
	}
	return string(out), stderr, err
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func mmToPx(mm float64) int { return int(math.Round(mm * 96 / 25.4)) }
func pxToMM(px int) float64 { return float64(px) * 25.4 / 96 }
func fileURL(path string) string {
	return "file://" + filepath.ToSlash(path)
}

func trimFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
