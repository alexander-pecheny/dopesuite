// Shared esbuild pipeline (root ADR-0001) as a pure-Go tool: `go -C
// scripts/webbuild run . [target...] [--watch]` builds the named targets
// (default: all). esbuild is a Go library, so the server dev path needs no
// JS runtime; deno/tsc enter only at the test gates.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

func entries(dir string, names ...string) []api.EntryPoint {
	out := make([]api.EntryPoint, 0, len(names))
	for _, n := range names {
		out = append(out, api.EntryPoint{InputPath: dir + n + ".ts", OutputPath: n})
	}
	return out
}

// xy ships native ES modules: every source transforms per-file (no bundling)
// so the emitted graph mirrors the source graph. sw.ts builds separately —
// it gets the derived precache manifest baked in (xySWBuild).
func xySources() []string {
	files, err := os.ReadDir("xy/web/ts")
	if err != nil {
		fatal(err.Error())
	}
	var out []string
	for _, f := range files {
		name := f.Name()
		if strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".d.ts") && name != "sw.ts" {
			out = append(out, "xy/web/ts/"+name)
		}
	}
	return out
}

// xyPrecache derives the service worker's app-shell manifest from the build
// graph and the shipped asset dirs, plus a content-hash cache version — the
// hand-typed list it replaces had already shipped one offline-504 regression
// (modules missing from the precache).
func xyPrecache() (urls []string, version string) {
	h := sha256.New()
	addURL := func(u string) {
		urls = append(urls, u)
		io.WriteString(h, u+"\x00")
	}
	hashFile := func(p string) {
		b, err := os.ReadFile(p)
		if err != nil {
			fatal(err.Error())
		}
		h.Write(b)
	}
	// skip names a file that ships but is NOT part of the shell: it still counts
	// toward the cache version (a rebuilt file must invalidate), it is just not
	// downloaded at install time.
	addDir := func(fsDir, urlPrefix string, skip func(string) bool) {
		ents, err := os.ReadDir(fsDir)
		if err != nil {
			fatal(err.Error())
		}
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			if skip == nil || !skip(e.Name()) {
				addURL(urlPrefix + e.Name())
			}
			hashFile(fsDir + "/" + e.Name())
		}
	}

	for _, r := range []string{"/", "/login", "/register", "/profile", "/profile/tokens", "/import"} {
		addURL(r)
	}
	addURL("/manifest.webmanifest")
	hashFile("xy/web/assets/static/manifest.webmanifest")
	addURL("/static/styles.css")
	hashFile("dopeuikit/assets/core.css")
	hashFile("xy/web/assets/static/styles.css")
	for _, src := range xySources() {
		name := strings.TrimSuffix(filepath.Base(src), ".ts")
		addURL("/static/dist/" + name + ".js")
		hashFile(src)
	}
	addURL("/static/menu.js")
	addURL("/static/login.js")
	// The kit modules xy imports rather than bundles (dist/kit/*.js). Their
	// sources are hashed with the rest of the kit's TS just below.
	for _, name := range xyKitModules {
		addURL("/static/dist/kit/" + name + ".js")
	}
	kitTS, err := filepath.Glob("dopeuikit/assets/ts/*.ts")
	if err != nil || len(kitTS) == 0 {
		fatal("no kit ts sources found for the precache hash")
	}
	for _, p := range kitTS {
		hashFile(p)
	}
	// The shell is what every install downloads, so it carries the default body
	// face and the mono and nothing else: the five a reader may switch to (the
	// profile's font picker) are ~1.3 MB together, and precaching them would hand
	// every reader five faces to save one of them a fetch. A reader who does pick
	// one fetches it once, and the runtime static rule caches it from there.
	addDir("dopeuikit/assets/fonts", "/static/fonts/", func(name string) bool {
		return !strings.HasPrefix(name, "noto-") && !strings.HasPrefix(name, "jetbrains-")
	})
	// Walk static/ recursively so a future subdirectory can't silently miss the
	// shell (the 504 class this derivation exists to kill). dist/ is skipped —
	// its URLs derive from xySources above; styles.css/manifest have their own
	// composite entries.
	err = filepath.WalkDir("xy/web/assets/static", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(filepath.ToSlash(path), "xy/web/assets/static")
		if d.IsDir() {
			if rel == "/dist" {
				return filepath.SkipDir
			}
			return nil
		}
		switch rel {
		case "/styles.css", "/manifest.webmanifest":
			return nil
		case "/favicon.ico":
			addURL("/favicon.ico")
		default:
			addURL("/static" + rel)
		}
		hashFile(path)
		return nil
	})
	if err != nil {
		fatal(err.Error())
	}
	return urls, "xy-shell-" + hex.EncodeToString(h.Sum(nil))[:10]
}

// xyKitModules are the kit's own TS modules xy loads as modules of its own.
// xy bundles nothing (every source transforms per-file so the emitted graph
// mirrors the source graph), so a shared module cannot be inlined the way dope
// and spliff inline it: it is built beside xy's, under dist/kit/, and imported
// from "./kit/<name>.js". web/ts/kit/<name>.d.ts is how tsc follows that URL.
var xyKitModules = []string{"suggest"}

func xySWBuild() api.BuildOptions {
	urls, version := xyPrecache()
	manifest, err := json.Marshal(urls)
	if err != nil {
		fatal(err.Error())
	}
	return api.BuildOptions{
		EntryPoints: []string{"xy/web/ts/sw.ts"},
		Format:      api.FormatESModule,
		Outdir:      "xy/web/assets/static/dist",
		Define: map[string]string{
			"__PRECACHE__":      string(manifest),
			"__SHELL_VERSION__": fmt.Sprintf("%q", version),
		},
	}
}

// builds is lazy so selecting one target never runs another's filesystem
// work — xySWBuild reads (and fatals on) xy+kit assets, which a dope-only
// build must not depend on.
type target struct {
	name   string
	builds func() []api.BuildOptions
}

func targets() []target {
	return []target{
		{"dope", func() []api.BuildOptions {
			return []api.BuildOptions{
				{
					EntryPointsAdvanced: entries("dope/dope/web/ts/pages/", "od", "si", "brain", "ek", "multi", "troika", "gallery"),
					Bundle:              true,
					Format:              api.FormatIIFE,
					Outdir:              "dope/dope/web/assets/static/dist",
				},
				// Builder-page classic scripts: self-contained IIFE bundles, one per script.
				{
					EntryPointsAdvanced: entries("dope/dope/web/ts/",
						"pageforms", "menu-config", "gamecreate", "numbers", "profile", "roster"),
					Bundle: true,
					Format: api.FormatIIFE,
					Outdir: "dope/dope/web/assets/static/dist",
				},
				// Library modules as ESM for the test runner (not embedded, not served).
				{
					EntryPointsAdvanced: entries("dope/dope/web/ts/",
						"entry-model", "sheet-cursor", "game-shell", "cells", "score-table", "venue", "standings", "fest-roster", "ek-stats", "state-sync", "game-page", "widgets", "stage-cache", "stats-sync", "fest-grid", "brain-stats", "group-stats", "game-tabs", "multi-protocol", "troika-protocol", "troika-stats", "crosstable",
						"od-protocol", "ksi-protocol", "brain-protocol", "screen-board",
						// game-page draws the 🏠 crumb through it
						"icons_gen",
						// the TS Catalog: the screens import i18nstrings, it the rest
						"i18nstrings", "i18nstrings_plural_gen", "i18nstrings_types_gen", "i18nstrings_ru_gen"),
					Format: api.FormatESModule,
					Outdir: "dope/dope/web/jstest/dist",
				},
			}
		}},
		// menu/login ship as classic bundles (menu must run blocking in <head> —
		// theme before first paint); the pure kernels also emit as ESM for tests.
		{"uikit", func() []api.BuildOptions {
			return []api.BuildOptions{
				{
					EntryPointsAdvanced: entries("dopeuikit/assets/ts/", "menu", "login"),
					Bundle:              true,
					Format:              api.FormatIIFE,
					Outdir:              "dopeuikit/assets/dist",
				},
				{
					EntryPointsAdvanced: entries("dopeuikit/assets/ts/", "menu-model", "login-model", "i18nstrings", "i18nstrings_plural_gen", "i18nstrings_ru_gen", "i18nstrings_en_gen"),
					Format:              api.FormatESModule,
					Outdir:              "dopeuikit/assets/dist/esm",
				},
			}
		}},
		// Spliff ships native ES modules like xy: one bundle per page, plus the
		// pure kernels as ESM for the deno tests.
		{"spliff", func() []api.BuildOptions {
			return []api.BuildOptions{
				{
					EntryPointsAdvanced: entries("spliff/spliff/web/ts/pages/", "index", "group", "transaction", "join", "profile"),
					Bundle:              true,
					Format:              api.FormatIIFE,
					Outdir:              "spliff/spliff/web/assets/static/dist",
				},
				{
					EntryPointsAdvanced: entries("spliff/spliff/web/ts/",
						"txform", "money", "profile-model", "currency-pick",
						// the TS Catalog: the pages import i18nstrings, it the rest
						"i18nstrings", "i18nstrings_plural_gen", "i18nstrings_types_gen", "i18nstrings_en_gen"),
					Format: api.FormatESModule,
					Outdir: "spliff/spliff/web/jstest/dist",
				},
			}
		}},
		{"xy", func() []api.BuildOptions {
			return []api.BuildOptions{
				{
					EntryPoints: xySources(),
					Format:      api.FormatESModule,
					Outdir:      "xy/web/assets/static/dist",
				},
				{
					EntryPointsAdvanced: entries("dopeuikit/assets/ts/", xyKitModules...),
					Format:              api.FormatESModule,
					Outdir:              "xy/web/assets/static/dist/kit",
				},
				xySWBuild(),
			}
		}},
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "webbuild: "+msg)
	os.Exit(1)
}

func main() {
	// Runs via `go -C scripts/webbuild run .`, whose cwd is the module dir;
	// build paths are repo-root-relative.
	if _, err := os.Stat("xy/web/ts"); err != nil {
		if err := os.Chdir("../.."); err != nil {
			fatal(err.Error())
		}
		if _, err := os.Stat("xy/web/ts"); err != nil {
			fatal("run from the repo root or via `go -C scripts/webbuild run .`")
		}
	}

	watch := false
	var names []string
	for _, arg := range os.Args[1:] {
		if arg == "--watch" {
			watch = true
			continue
		}
		if strings.HasPrefix(arg, "--") {
			fatal("unknown flag " + arg)
		}
		names = append(names, arg)
	}

	all := targets()
	byName := map[string]func() []api.BuildOptions{}
	for _, t := range all {
		byName[t.name] = t.builds
	}
	if len(names) == 0 {
		for _, t := range all {
			names = append(names, t.name)
		}
	}
	for _, name := range names {
		builds, ok := byName[name]
		if !ok {
			fatal("unknown target: " + name)
		}
		for _, build := range builds() {
			build.LogLevel = api.LogLevelInfo
			build.Target = api.ES2019
			build.Sourcemap = api.SourceMapLinked
			build.Write = true
			// Served bundles ship minified; the test-runner outputs (jstest/,
			// dist/esm) are never served, so they stay readable. ESM export
			// names survive minification — jstest imports the served xy dist.
			if !strings.Contains(build.Outdir, "jstest") && !strings.HasSuffix(build.Outdir, "/esm") {
				build.MinifyWhitespace = true
				build.MinifySyntax = true
				build.MinifyIdentifiers = true
			}
			if watch {
				ctx, err := api.Context(build)
				if err != nil {
					os.Exit(1)
				}
				if watchErr := ctx.Watch(api.WatchOptions{}); watchErr != nil {
					fatal(watchErr.Error())
				}
			} else if result := api.Build(build); len(result.Errors) > 0 {
				os.Exit(1)
			}
		}
	}
	if watch {
		select {}
	}
}
