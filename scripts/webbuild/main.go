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
	addDir := func(fsDir, urlPrefix string) {
		ents, err := os.ReadDir(fsDir)
		if err != nil {
			fatal(err.Error())
		}
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			addURL(urlPrefix + e.Name())
			hashFile(fsDir + "/" + e.Name())
		}
	}

	for _, r := range []string{"/", "/login", "/register", "/profile", "/import"} {
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
	kitTS, err := filepath.Glob("dopeuikit/assets/ts/*.ts")
	if err != nil || len(kitTS) == 0 {
		fatal("no kit ts sources found for the precache hash")
	}
	for _, p := range kitTS {
		hashFile(p)
	}
	addDir("xy/web/assets/static/vendor", "/static/vendor/")
	addDir("dopeuikit/assets/fonts", "/static/fonts/")
	ents, err := os.ReadDir("xy/web/assets/static")
	if err != nil {
		fatal(err.Error())
	}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || name == "styles.css" || name == "manifest.webmanifest" {
			continue
		}
		if name == "favicon.ico" {
			addURL("/favicon.ico")
		} else {
			addURL("/static/" + name)
		}
		hashFile("xy/web/assets/static/" + name)
	}
	return urls, "xy-shell-" + hex.EncodeToString(h.Sum(nil))[:10]
}

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

type target struct {
	name   string
	builds []api.BuildOptions
}

func targets() []target {
	return []target{
		{"dope", []api.BuildOptions{
			{
				EntryPointsAdvanced: entries("dope/dope/web/ts/pages/", "od", "si", "host", "viewer"),
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
					"entry-model", "match-table", "state-sync", "stage-cache", "stats-sync", "fest-grid"),
				Format: api.FormatESModule,
				Outdir: "dope/dope/web/jstest/dist",
			},
		}},
		// menu/login ship as classic bundles (menu must run blocking in <head> —
		// theme before first paint); the pure kernels also emit as ESM for tests.
		{"uikit", []api.BuildOptions{
			{
				EntryPointsAdvanced: entries("dopeuikit/assets/ts/", "menu", "login"),
				Bundle:              true,
				Format:              api.FormatIIFE,
				Outdir:              "dopeuikit/assets/dist",
			},
			{
				EntryPointsAdvanced: entries("dopeuikit/assets/ts/", "menu-model", "login-model"),
				Format:              api.FormatESModule,
				Outdir:              "dopeuikit/assets/dist/esm",
			},
		}},
		{"xy", []api.BuildOptions{
			{
				EntryPoints: xySources(),
				Format:      api.FormatESModule,
				Outdir:      "xy/web/assets/static/dist",
			},
			xySWBuild(),
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
	byName := map[string][]api.BuildOptions{}
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
		for _, build := range builds {
			build.LogLevel = api.LogLevelInfo
			build.Target = api.ES2019
			build.Sourcemap = api.SourceMapLinked
			build.Write = true
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
