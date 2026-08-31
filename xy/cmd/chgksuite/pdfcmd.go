package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"xy/internal/chgk/handout"
	"xy/internal/chgk/typstdoc"
	"xy/internal/chgk/typstwasm"
)

// composePDF is `chgksuite compose pdf`: the same document the docx export
// makes, typeset by typst. chgksuite downloads a typst binary the first time it
// needs one; this one carries typst compiled to wasm and runs it in-process.
func composePDF(args []string) error {
	fs := flag.NewFlagSet("compose pdf", flag.ContinueOnError)
	device := fs.String("device", "desktop", "page size: desktop (A4) or mobile (phone-screen-sized)")
	configPath := fs.String("pdf_config", "", "a typography config of your own; empty is the one chgksuite ships")
	font := fs.String("font", override("font", override("font_face", "")), "font family; empty is the bundled Noto Sans")
	language := languageFlag(fs)
	rawTypst := fs.Bool("rawtypst", false, "write the typst source beside the PDF")
	addTS := fs.String("add_ts", override("add_ts", "off"), "append a timestamp to the output filename: on|off")
	merge := fs.Bool("merge", false, "export the input files as one package")
	noBreak := noBreakFlags(fs)
	config := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := applyConfig(fs, *config); err != nil {
		return err
	}
	if *device != "desktop" && *device != "mobile" {
		return fmt.Errorf("--device: %q is not desktop or mobile", *device)
	}
	lang, labelsFile, err := language()
	if err != nil {
		return err
	}
	opts := typstdoc.Options{
		Device:     typstdoc.Device(*device),
		Font:       *font,
		Language:   lang,
		LabelsFile: labelsFile,
		NoBreak:    noBreak(),
	}
	if *configPath != "" {
		raw, err := os.ReadFile(*configPath)
		if err != nil {
			return err
		}
		if opts.Config, err = typstdoc.ParseConfig(string(raw)); err != nil {
			return fmt.Errorf("%s: %w", *configPath, err)
		}
	}
	sources, err := loadSources(fs.Args(), *merge)
	if err != nil {
		return err
	}

	ctx := context.Background()
	fonts, err := handout.BundledFonts()
	if err != nil {
		return err
	}
	pool, err := typstwasm.NewPool(ctx, fonts, wasmCacheDir(), 1)
	if err != nil {
		return err
	}
	defer pool.Close()

	suffix := ""
	if *device == "mobile" {
		suffix = "_mobile"
	}
	for _, s := range sources {
		images, err := loadImages(s.doc, s.dir)
		if err != nil {
			return err
		}
		if *rawTypst {
			typ := outputName(s.path, "typ", suffix, *addTS == "on")
			if err := os.WriteFile(typ, []byte(typstdoc.GenerateTyp(s.doc, images, opts)), 0o644); err != nil {
				return err
			}
			fmt.Println("Output:", typ)
		}
		data, err := typstdoc.Export(ctx, s.doc, images, pool, opts)
		if err != nil {
			return err
		}
		out := outputName(s.path, "pdf", suffix, *addTS == "on")
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		fmt.Println("Output:", out)
	}
	return nil
}

// wasmCacheDir is where wazero keeps typst compiled to machine code: a cold
// compile is ~15s and a warm one half a second, and the cache survives a reboot
// only if it is not on tmpfs. It holds compiled typst, never a package.
func wasmCacheDir() string {
	if dir := os.Getenv("XY_WASM_CACHE"); dir != "" {
		return dir
	}
	if base, err := os.UserCacheDir(); err == nil {
		return filepath.Join(base, "xy", "typst-wasm")
	}
	return filepath.Join(os.TempDir(), "xy-typst-wasm")
}
