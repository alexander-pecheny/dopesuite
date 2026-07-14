package server

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"xy/internal/chgk/handout"
	"xy/internal/chgk/typstwasm"
)

// typst runs in-process as a wasm module (internal/chgk/typstwasm), so handout
// rendering never hands the user's decrypted questions to a filesystem — the last
// place in xy where that was unavoidable, since the typst CLI is a separate process
// that can only read files.
//
// The pool is built once and reused: compiling the module is the expensive part.

// typstPoolSize bounds concurrent renders. split_fit fits its blocks in parallel,
// so a single instance would serialise the slowest path we have; each instance
// costs its own linear memory, so this is capped rather than unbounded.
func typstPoolSize() int {
	n := runtime.NumCPU()
	if n > 4 {
		n = 4
	}
	if n < 1 {
		n = 1
	}
	return n
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

// typesetter returns the shared typst pool, building it on first use. Main also
// warms it in the background at boot, so a user rarely waits for it.
func (s *server) typesetter() (handout.Typesetter, error) {
	s.typstOnce.Do(func() {
		if s.typst != nil {
			return // injected (tests)
		}
		start := time.Now()
		fonts, err := handout.BundledFonts()
		if err != nil {
			s.typstErr = err
			return
		}
		pool, err := typstwasm.NewPool(context.Background(), fonts, wasmCacheDir(), typstPoolSize())
		if err != nil {
			s.typstErr = err
			return
		}
		s.typst = pool
		log.Printf("typst (wasm) ready in %v, %d instances", time.Since(start).Round(time.Millisecond), typstPoolSize())
	})
	if s.typstErr != nil {
		return nil, s.typstErr
	}
	return s.typst, nil
}

// warmTypst builds the pool ahead of the first request. Cold, that is a ~15s wasm
// compile (a fresh host, or after a wazero upgrade invalidates the cache) — which
// no user should have to sit through.
func (s *server) warmTypst() {
	go func() {
		if _, err := s.typesetter(); err != nil {
			log.Printf("typst (wasm) unavailable: %v", err)
		}
	}()
}
