// Package icons is the suite's single source of icon shapes. It vendors the
// Lucide set (lucide.dev, ISC — see LICENSE.lucide) as the .svg files in svg/,
// and hands out their inner markup by name, so nothing downstream writes path
// data by hand.
//
// Emoji used to do this job. They render in each platform's own colours and
// ignore the theme entirely, so a dark board showed a glossy light-mode 🗑️, and
// the same button looked like a different button on macOS, Android and Linux.
// An inline SVG stroked in currentColor is simply the button's text.
//
// Only icons actually used are vendored: `just icons-add <name>` is the way to
// add one, and the generator refuses a name it has no file for, so a typo in a
// page is a build failure rather than a blank square.
//
//go:generate go run ../cmd/icongen -go icons_gen.go -vocab ../kit/vocab.json -ts ../assets/ts/icons_gen.ts -ts ../../xy/web/ts/icons_gen.ts -ts ../../dope/dope/web/ts/icons_gen.ts
package icons

import "sort"

// Body returns the inner markup of the named icon (the <path>/<circle>… run,
// without the <svg> wrapper), and whether it exists. The wrapper is written by
// whoever draws it — the Go expander for compiled pages, icons_gen.ts for
// buttons built in the browser — so both can size and label it their own way.
func Body(name string) (string, bool) {
	b, ok := bodies[name]
	return b, ok
}

// Names lists every vendored icon, sorted. The .dopeui validator uses it to
// close the `icon` vocabulary, so an unknown name is a compile error.
func Names() []string {
	out := make([]string, 0, len(bodies))
	for n := range bodies {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
