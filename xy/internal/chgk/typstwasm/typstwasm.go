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
// and cross-compilable — which linking typst natively via cgo would have cost.
//
// The guest is a WASI *reactor*: an instance keeps its fonts and images across
// calls, so the fonts are parsed once and the images once per generation. split_fit
// binary-searches the row count and so compiles the same document dozens of times;
// with the CLI every one of those probes is a fresh process that re-reads the fonts.
package typstwasm

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// typstWasm is the compiled guest (see typst-wasm/, built with
// `cargo build --release --target wasm32-wasip1`). It is vendored like the fonts,
// so the Go build needs no Rust toolchain.
//
//go:embed typst.wasm
var typstWasm []byte

// Pool is a set of typst instances. It implements handout.Typesetter.
//
// A single instance would serialise everything: the guest keeps its fonts and
// images in globals, so one compile at a time. split_fit fits its blocks in
// parallel, so the pool holds one instance per worker. Compiling the wasm to
// machine code happens once and is shared; instantiating is comparatively cheap.
type Pool struct {
	rt        wazero.Runtime
	instances []*instance
	free      chan *instance
	closeCtx  context.Context
}

type instance struct {
	mod     api.Module
	alloc   api.Function
	dealloc api.Function
	addFont api.Function
	addFile api.Function
	reset   api.Function
	compile api.Function
}

// NewPool compiles the module once, instantiates `size` copies and loads the fonts
// into each.
//
// cacheDir is where wazero keeps the compiled machine code. It matters a lot:
// compiling 30 MB of wasm takes ~15s cold but ~0.5s from the cache, and the cache
// survives restarts. It must live on PERSISTENT storage — on tmpfs it is wiped on
// reboot and the cold cost comes back. It holds compiled typst, no user data.
func NewPool(ctx context.Context, fonts [][]byte, cacheDir string, size int) (*Pool, error) {
	if size < 1 {
		size = 1
	}
	cfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	if cacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(cacheDir)
		if err != nil {
			return nil, fmt.Errorf("compilation cache: %w", err)
		}
		cfg = cfg.WithCompilationCache(cache)
	}
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	fail := func(err error) (*Pool, error) {
		rt.Close(ctx)
		return nil, err
	}
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return fail(fmt.Errorf("wasi: %w", err))
	}
	// Compile once; every instance below is stamped from this.
	compiled, err := rt.CompileModule(ctx, typstWasm)
	if err != nil {
		return fail(fmt.Errorf("compile module: %w", err))
	}

	p := &Pool{rt: rt, closeCtx: ctx, free: make(chan *instance, size)}
	for i := range size {
		// No filesystem, no env, no args: the guest could not reach the host's disk
		// even if typst tried to, which is the whole point of the exercise.
		mod, err := rt.InstantiateModule(ctx, compiled,
			wazero.NewModuleConfig().
				WithName("typst"+strconv.Itoa(i)).
				WithStartFunctions("_initialize"))
		if err != nil {
			return fail(fmt.Errorf("instantiate: %w", err))
		}
		in := &instance{
			mod:     mod,
			alloc:   mod.ExportedFunction("alloc"),
			dealloc: mod.ExportedFunction("dealloc"),
			addFont: mod.ExportedFunction("add_font"),
			addFile: mod.ExportedFunction("add_file"),
			reset:   mod.ExportedFunction("reset_files"),
			compile: mod.ExportedFunction("compile"),
		}
		for name, f := range map[string]api.Function{
			"alloc": in.alloc, "dealloc": in.dealloc, "add_font": in.addFont,
			"add_file": in.addFile, "reset_files": in.reset, "compile": in.compile,
		} {
			if f == nil {
				return fail(fmt.Errorf("guest is missing export %q", name))
			}
		}
		for _, font := range fonts {
			ptr, err := in.write(ctx, font)
			if err != nil {
				return fail(err)
			}
			_, err = in.addFont.Call(ctx, uint64(ptr), uint64(len(font)))
			in.free(ctx, ptr, uint32(len(font)))
			if err != nil {
				return fail(fmt.Errorf("add_font: %w", err))
			}
		}
		p.instances = append(p.instances, in)
		p.free <- in
	}
	return p, nil
}

func (p *Pool) Close() error { return p.rt.Close(p.closeCtx) }

// SetImages replaces the images the source may reference, keyed by the bare name
// the (img …) directive uses. Every instance gets them, since any one of them may
// serve the next Compile.
func (p *Pool) SetImages(ctx context.Context, images map[string][]byte) error {
	// Drain the pool so no compile is running against half-replaced images.
	held := make([]*instance, 0, len(p.instances))
	defer func() {
		for _, in := range held {
			p.free <- in
		}
	}()
	for range p.instances {
		select {
		case in := <-p.free:
			held = append(held, in)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, in := range held {
		if _, err := in.reset.Call(ctx); err != nil {
			return fmt.Errorf("reset_files: %w", err)
		}
		for name, data := range images {
			if err := in.addImage(ctx, name, data); err != nil {
				return err
			}
		}
	}
	return nil
}

// Compile typesets typ on a free instance. wantPDF=false skips PDF generation and
// only reports the page count — all split_fit's binary search needs.
func (p *Pool) Compile(ctx context.Context, typ string, wantPDF bool) ([]byte, int, error) {
	var in *instance
	select {
	case in = <-p.free:
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	defer func() { p.free <- in }()
	return in.compileOn(ctx, typ, wantPDF)
}

// ── one instance ──

func (in *instance) write(ctx context.Context, b []byte) (uint32, error) {
	res, err := in.alloc.Call(ctx, uint64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("alloc: %w", err)
	}
	ptr := uint32(res[0])
	if !in.mod.Memory().Write(ptr, b) {
		return 0, errors.New("write outside guest memory")
	}
	return ptr, nil
}

func (in *instance) free(ctx context.Context, ptr, size uint32) {
	_, _ = in.dealloc.Call(ctx, uint64(ptr), uint64(size))
}

func (in *instance) addImage(ctx context.Context, name string, data []byte) error {
	np, err := in.write(ctx, []byte(name))
	if err != nil {
		return err
	}
	dp, err := in.write(ctx, data)
	if err != nil {
		in.free(ctx, np, uint32(len(name)))
		return err
	}
	_, err = in.addFile.Call(ctx, uint64(np), uint64(len(name)), uint64(dp), uint64(len(data)))
	in.free(ctx, np, uint32(len(name)))
	in.free(ctx, dp, uint32(len(data)))
	if err != nil {
		return fmt.Errorf("add_file: %w", err)
	}
	return nil
}

func (in *instance) compileOn(ctx context.Context, typ string, wantPDF bool) ([]byte, int, error) {
	src, err := in.write(ctx, []byte(typ))
	if err != nil {
		return nil, 0, err
	}
	defer in.free(ctx, src, uint32(len(typ)))

	var want uint64
	if wantPDF {
		want = 1
	}
	res, err := in.compile.Call(ctx, uint64(src), uint64(len(typ)), want)
	if err != nil {
		return nil, 0, fmt.Errorf("compile: %w", err)
	}
	// The guest packs its result buffer as (ptr << 32) | len.
	ptr, size := uint32(res[0]>>32), uint32(res[0])
	out, ok := in.mod.Memory().Read(ptr, size)
	if !ok {
		return nil, 0, errors.New("result outside guest memory")
	}
	buf := make([]byte, len(out))
	copy(buf, out) // the guest frees the original next
	in.free(ctx, ptr, size)

	if len(buf) < 5 {
		return nil, 0, errors.New("short result")
	}
	pages := int(binary.LittleEndian.Uint32(buf[1:5]))
	if buf[0] != 1 {
		return nil, pages, fmt.Errorf("typst: %s", buf[5:])
	}
	return buf[5:], pages, nil
}
