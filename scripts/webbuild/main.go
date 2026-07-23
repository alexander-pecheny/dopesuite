// Shared esbuild pipeline (root ADR-0001) as a pure-Go tool: `go -C
// scripts/webbuild run . [target...] [--watch]` builds the named targets
// (default: all). esbuild is a Go library, so the server dev path needs no
// JS runtime; deno/tsc enter only at the test gates.
package main

import (
	"fmt"
	"os"
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
// so the emitted graph mirrors the source graph.
func xySources() []string {
	files, err := os.ReadDir("xy/web/ts")
	if err != nil {
		fatal(err.Error())
	}
	var out []string
	for _, f := range files {
		name := f.Name()
		if strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".d.ts") {
			out = append(out, "xy/web/ts/"+name)
		}
	}
	return out
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
					"entry-model", "match-table", "stage-cache", "stats-sync", "fest-grid"),
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
				EntryPointsAdvanced: entries("dopeuikit/assets/ts/", "menu-model"),
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
