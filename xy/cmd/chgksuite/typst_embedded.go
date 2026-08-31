//go:build wasmtypst

package main

import (
	"context"

	"xy/internal/chgk/handout"
	"xy/internal/chgk/typstwasm"
)

// The file is not called typst_wasm.go on purpose: Go reads a _wasm suffix as
// GOARCH=wasm and would build it for nothing else.
//
// Built with -tags wasmtypst: typst rides along as a wasm module, so the command
// renders with nothing installed. It costs ~33 MB of binary.

func wasmTypesetter() (handout.Typesetter, func(), error) {
	fonts, err := handout.BundledFonts()
	if err != nil {
		return nil, nil, err
	}
	pool, err := typstwasm.NewPool(context.Background(), fonts, wasmCacheDir(), 1)
	if err != nil {
		return nil, nil, err
	}
	return pool, func() { pool.Close() }, nil
}

const wasmBuiltIn = true
