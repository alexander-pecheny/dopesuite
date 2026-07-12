// Package typstwasm runs typst in-process, as a WebAssembly module, with its
// whole filesystem served from memory.
//
// Why it exists: handout rendering is the only part of xy that has to hand the
// user's decrypted questions to a filesystem, because the typst CLI is a separate
// process that reads its source and images as files. typst's `World` trait is an
// abstract filesystem, so linking typst in as a library and answering those reads
// from a map removes the requirement outright — the plaintext never leaves RAM.
//
// It runs under wazero, a pure-Go WASM runtime, so the server stays CGO_ENABLED=0
// and cross-compilable (which linking typst natively via cgo would have cost).
//
// The module is a WASI reactor: it is instantiated once and keeps its state, so
// the fonts are parsed once per process and the images once per generation.
// split_fit compiles the same document dozens of times while binary-searching the
// row count, and with the CLI every one of those probes is a fresh process that
// re-reads the fonts. Here they are already warm.
package typstwasm

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// typstWasm is the compiled guest (see typst-wasm/, built with
// `cargo build --release --target wasm32-wasip1`). Embedding it keeps the Go build
// free of any Rust toolchain — the artifact is vendored like the fonts.
//
//go:embed typst.wasm
var typstWasm []byte

// Engine is one instantiated typst. It is NOT safe for concurrent use: the guest
// holds the loaded fonts and images in globals, so callers must serialise (the
// mutex below does that) or hold one Engine per concurrent render.
type Engine struct {
	mu       sync.Mutex
	rt       wazero.Runtime
	mod      api.Module
	alloc    api.Function
	dealloc  api.Function
	addFont  api.Function
	addFile  api.Function
	reset    api.Function
	compile  api.Function
	closeCtx context.Context
}

// New compiles the module and loads the fonts.
//
// Compiling 30 MB of wasm to machine code takes ~15s, so cacheDir matters: wazero
// keeps the compiled code there and reuses it on the next start. Pass "" to skip
// the cache (and pay the full compile every time). The cache holds compiled typst,
// not user data, so it is safe on ordinary disk.
func New(ctx context.Context, fonts [][]byte, cacheDir string) (*Engine, error) {
	cfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	if cacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(cacheDir)
		if err != nil {
			return nil, fmt.Errorf("compilation cache: %w", err)
		}
		cfg = cfg.WithCompilationCache(cache)
	}
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("wasi: %w", err)
	}
	// No filesystem, no env, no args: the guest cannot reach the host's disk even
	// if typst tried to, which is the whole point.
	mod, err := rt.InstantiateWithConfig(ctx, typstWasm,
		wazero.NewModuleConfig().WithName("typst").WithStartFunctions("_initialize"))
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("instantiate: %w", err)
	}
	e := &Engine{
		rt: rt, mod: mod, closeCtx: ctx,
		alloc:   mod.ExportedFunction("alloc"),
		dealloc: mod.ExportedFunction("dealloc"),
		addFont: mod.ExportedFunction("add_font"),
		addFile: mod.ExportedFunction("add_file"),
		reset:   mod.ExportedFunction("reset_files"),
		compile: mod.ExportedFunction("compile"),
	}
	for name, f := range map[string]api.Function{
		"alloc": e.alloc, "dealloc": e.dealloc, "add_font": e.addFont,
		"add_file": e.addFile, "reset_files": e.reset, "compile": e.compile,
	} {
		if f == nil {
			rt.Close(ctx)
			return nil, fmt.Errorf("guest is missing export %q", name)
		}
	}
	for _, font := range fonts {
		ptr, err := e.write(ctx, font)
		if err != nil {
			rt.Close(ctx)
			return nil, err
		}
		if _, err := e.addFont.Call(ctx, uint64(ptr), uint64(len(font))); err != nil {
			rt.Close(ctx)
			return nil, fmt.Errorf("add_font: %w", err)
		}
		e.free(ctx, ptr, uint32(len(font)))
	}
	return e, nil
}

func (e *Engine) Close() error { return e.rt.Close(e.closeCtx) }

// write copies b into the guest's linear memory and returns its offset.
func (e *Engine) write(ctx context.Context, b []byte) (uint32, error) {
	res, err := e.alloc.Call(ctx, uint64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("alloc: %w", err)
	}
	ptr := uint32(res[0])
	if !e.mod.Memory().Write(ptr, b) {
		return 0, errors.New("write outside guest memory")
	}
	return ptr, nil
}

func (e *Engine) free(ctx context.Context, ptr, size uint32) {
	_, _ = e.dealloc.Call(ctx, uint64(ptr), uint64(size))
}

// SetImages replaces the images the source may reference, keyed by the bare name
// the (img …) directive uses.
func (e *Engine) SetImages(ctx context.Context, images map[string][]byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.reset.Call(ctx); err != nil {
		return fmt.Errorf("reset_files: %w", err)
	}
	for name, data := range images {
		np, err := e.write(ctx, []byte(name))
		if err != nil {
			return err
		}
		dp, err := e.write(ctx, data)
		if err != nil {
			return err
		}
		_, err = e.addFile.Call(ctx, uint64(np), uint64(len(name)), uint64(dp), uint64(len(data)))
		e.free(ctx, np, uint32(len(name)))
		e.free(ctx, dp, uint32(len(data)))
		if err != nil {
			return fmt.Errorf("add_file: %w", err)
		}
	}
	return nil
}

// Compile typesets typ. wantPDF=false skips PDF generation and only reports the
// page count — which is all split_fit's binary search needs.
func (e *Engine) Compile(ctx context.Context, typ string, wantPDF bool) (pdf []byte, pages int, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	src, err := e.write(ctx, []byte(typ))
	if err != nil {
		return nil, 0, err
	}
	defer e.free(ctx, src, uint32(len(typ)))

	var want uint64
	if wantPDF {
		want = 1
	}
	res, err := e.compile.Call(ctx, uint64(src), uint64(len(typ)), want)
	if err != nil {
		return nil, 0, fmt.Errorf("compile: %w", err)
	}
	// The guest packs its result buffer as (ptr << 32) | len.
	packed := res[0]
	ptr, size := uint32(packed>>32), uint32(packed)
	out, ok := e.mod.Memory().Read(ptr, size)
	if !ok {
		return nil, 0, errors.New("result outside guest memory")
	}
	buf := make([]byte, len(out))
	copy(buf, out) // the guest frees the original on the next line
	e.free(ctx, ptr, size)

	if len(buf) < 5 {
		return nil, 0, errors.New("short result")
	}
	pages = int(binary.LittleEndian.Uint32(buf[1:5]))
	if buf[0] != 1 {
		return nil, pages, fmt.Errorf("typst: %s", buf[5:])
	}
	return buf[5:], pages, nil
}
