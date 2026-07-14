package server

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// fakeTypesetter stands in for typst in the handler tests. They are about the HTTP
// plumbing — that the source arrives intact and the right images come along — not
// about typesetting, so stubbing it keeps them fast (no 30 MB wasm compile) and
// lets them assert on what typst was actually handed.
type fakeTypesetter struct {
	mu     sync.Mutex
	typ    string
	images map[string][]byte
}

func (f *fakeTypesetter) SetImages(_ context.Context, images map[string][]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images = images
	return nil
}

func (f *fakeTypesetter) Compile(_ context.Context, typ string, _ bool) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typ = typ
	return []byte("%PDF-fake"), 1, nil
}

// source returns the .typ typst was last given.
func (f *fakeTypesetter) source() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.typ
}

// hasImage reports whether an image of that name was handed to typst.
func (f *fakeTypesetter) hasImage(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.images[name]
	return ok
}

// blocks counts the handout blocks in the generated source.
func (f *fakeTypesetter) blocks() int {
	return strings.Count(f.source(), "#handout(")
}

// stubTypst injects the fake and returns it.
func stubTypst(t *testing.T, srv *server) *fakeTypesetter {
	t.Helper()
	f := &fakeTypesetter{}
	srv.typst = f
	return f
}
