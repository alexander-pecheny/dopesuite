// Command uigen generates a typed Go builder from a vocabulary.
//
//	uigen -core vocab.json -pkg kit -base pecheny.me/dopeuikit/ui -out tags_gen.go
//	uigen -core core.json -overlay app.json -pkg ui -base pecheny.me/dopeuikit/kit -out tags_gen.go
//
// Core mode emits the kit's builder over the ui engine (a constructor for every
// primitive). Overlay mode emits an app package: the overlay's own
// constructors/consts plus re-export shims for every base (kit) constructor/const.
// Both read their vocabularies from disk (the tool imports no design-system
// package, so it can regenerate the kit's own tags_gen.go).
package main

import (
	"flag"
	"fmt"
	"os"

	"pecheny.me/dopeuikit/ui"
	"pecheny.me/dopeuikit/ui/uigen"
)

func main() {
	core := flag.String("core", "vocab.json", "core/base vocab.json path")
	overlay := flag.String("overlay", "", "overlay vocab.json path (overlay mode)")
	pkg := flag.String("pkg", "kit", "emitted package name")
	base := flag.String("base", "pecheny.me/dopeuikit/ui", "base import path")
	out := flag.String("out", "tags_gen.go", "output .go path")
	flag.Parse()

	src, err := gen(*core, *overlay, *pkg, *base)
	if err != nil {
		die(err)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		die(err)
	}
}

func gen(corePath, overlayPath, pkg, base string) ([]byte, error) {
	coreVocab, err := loadVocab(corePath)
	if err != nil {
		return nil, err
	}
	if overlayPath == "" {
		return uigen.Core(coreVocab, pkg, base)
	}
	overlayData, err := os.ReadFile(overlayPath)
	if err != nil {
		return nil, err
	}
	merged, err := coreVocab.Merge(overlayData)
	if err != nil {
		return nil, err
	}
	return uigen.Overlay(coreVocab, merged, pkg, base)
}

func loadVocab(path string) (*ui.Vocab, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ui.LoadVocab(data)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "uigen:", err)
	os.Exit(1)
}
