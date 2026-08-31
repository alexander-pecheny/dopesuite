//go:build !wasmtypst

package main

import (
	"fmt"

	"xy/internal/chgk/handout"
)

// The default build. typst is not carried along — it is an ordinary program,
// this is an ordinary CLI, and embedding a second copy of it costs ~33 MB for
// something most machines already have or can install in a minute.
//
// Build with -tags wasmtypst to embed it anyway, which is what a single-file
// distribution wants.

func wasmTypesetter() (handout.Typesetter, func(), error) {
	return nil, nil, fmt.Errorf(
		"no typst found: install it (https://typst.app), name it with --typst or " +
			"$CHGKSUITE_TYPST, or rebuild with -tags wasmtypst to carry one along")
}

const wasmBuiltIn = false
