package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"

	"xy/internal/chgk/handout"
)

// How typst runs: the ordinary typst release, which is what a shell user is
// likely to want. It starts in milliseconds where the wasm has to be compiled to
// machine code first (~15s cold, ~0.5s from the cache), and it sees the
// machine's own fonts, so --font can name one.
//
// The wasm — xy's server's typst, linked in so that a decrypted question never
// reaches a filesystem — is the fallback, and only in a build that asked for it
// (-tags wasmtypst). Here the package is a file on the user's own disk already,
// and carrying a second typst costs ~33 MB.

// typstFlag declares --typst on a command that renders.
func typstFlag(fs *flag.FlagSet) *string {
	help := "the typst binary to render with; empty looks for one on PATH"
	if wasmBuiltIn {
		help += " and falls back to the built-in wasm"
	}
	return fs.String("typst", override("typst", ""), help)
}

// typesetter picks between them. The returned func releases whichever was used.
func typesetter(bin string) (handout.Typesetter, func(), error) {
	if bin == "" {
		bin = os.Getenv("CHGKSUITE_TYPST")
	}
	if bin == "" {
		if found, err := exec.LookPath("typst"); err == nil {
			bin = found
		}
	}
	if bin != "" {
		ts, err := handout.NewCLITypesetterWith(handout.CLIOptions{
			Bin: bin, SystemFonts: true,
			Warn: func(line string) { warn("typst: %s", line) },
		})
		if err != nil {
			return nil, nil, err
		}
		return ts, func() { ts.Close() }, nil
	}
	return wasmTypesetter()
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
