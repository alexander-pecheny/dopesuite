//go:build !nowasmtypst

package server

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/handout"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/typstwasm"
)

// The pool is built once and reused: compiling the module is the expensive part.
// The desktop app builds with -tags nowasmtypst and leaves the 30 MB module out.

// maxTypstPool is the most typst instances the pool holds.
const maxTypstPool = 4

// typstPoolSize bounds concurrent renders. split_fit fits its blocks in parallel,
// so a single instance would serialise the slowest path we have; each instance
// costs its own linear memory, so this is capped rather than unbounded.
func typstPoolSize() int {
	return max(1, min(runtime.NumCPU(), maxTypstPool))
}

func newTypstPool() (handout.Typesetter, error) {
	fonts, err := handout.BundledFonts()
	if err != nil {
		return nil, err
	}
	start := time.Now()
	pool, err := typstwasm.NewPool(context.Background(), fonts, wasmCacheDir(), typstPoolSize())
	if err != nil {
		return nil, err
	}
	log.Printf("typst (wasm) ready in %v, %d instances", time.Since(start).Round(time.Millisecond), typstPoolSize())
	return pool, nil
}

// wasmCacheDir is where wazero keeps typst compiled to machine code. This must be
// PERSISTENT storage: the compile takes ~15s cold but ~0.5s from the cache, and the
// cache survives restarts — put it on tmpfs and a reboot brings the 15s back. It
// holds compiled typst, never user data, so ordinary disk is right.
func wasmCacheDir() string {
	if dir := os.Getenv("XY_WASM_CACHE"); dir != "" {
		return dir
	}
	if base, err := os.UserCacheDir(); err == nil {
		return filepath.Join(base, "xy", "typst-wasm")
	}
	return filepath.Join(os.TempDir(), "xy-typst-wasm")
}
