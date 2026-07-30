// palettegen writes the vendored uchu ramps into core.css, between the two
// GENERATED markers in its palette block. Nothing else in the repo may write a
// neutral hex value: the ramps are the ladder every theme indexes into, so a
// hand-edited rung is drift the ladder cannot see.
//
//	go run ./cmd/palettegen -css ../dopeuikit/assets/core.css
//
// Run through `go generate ./palette` from dopeuikit/, and checked by the
// root justfile's generate-check.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"pecheny.me/dopeuikit/palette"
)

func main() {
	cssPath := flag.String("css", "assets/core.css", "path to core.css")
	flag.Parse()

	src, err := os.ReadFile(*cssPath)
	if err != nil {
		die(err)
	}
	body := string(src)

	begin := strings.Index(body, palette.BeginMarker)
	end := strings.Index(body, palette.EndMarker)
	if begin < 0 || end < 0 || end < begin {
		die(fmt.Errorf("%s: missing or inverted GENERATED markers", *cssPath))
	}

	out := body[:begin+len(palette.BeginMarker)] + "\n" + ramps() + "  " + body[end:]
	if out == body {
		return
	}
	if err := os.WriteFile(*cssPath, []byte(out), 0o644); err != nil {
		die(err)
	}
}

// ramps emits every rung of both variants. All 164 ship, not just the ones a
// theme currently names: the whole point of a numbered ladder is that shifting a
// role one rung is an edit to one integer, and that only holds if the neighbour
// is already there. Gzip collapses the repetition to about a kilobyte.
func ramps() string {
	var b strings.Builder
	for _, name := range palette.Anchors() {
		fmt.Fprintf(&b, "  --uchu-%s: %s;\n", name, palette.Anchor(name).CSS())
	}
	for _, variant := range palette.Variants() {
		prefix := ""
		if variant == "pastel" {
			prefix = "pastel-"
		}
		for _, hue := range palette.Hues(variant) {
			b.WriteByte('\n')
			for n := 1; n <= 9; n++ {
				fmt.Fprintf(&b, "  --uchu-%s%s-%d: %s;\n", prefix, hue, n, palette.Rung(variant, hue, n).CSS())
			}
		}
	}
	return b.String()
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "palettegen:", err)
	os.Exit(1)
}
