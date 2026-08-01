package kit

import (
	"fmt"

	"pecheny.me/dopeuikit/icons"
)

// iconItem renders the `icon="…"` prop of a control as an inline SVG, or nil
// when the control has none. Compiled into the page rather than swapped in by a
// boot script: an emoji placeholder would be visible until that script ran, and
// the name would be invisible to the compiler.
//
// The name is validated against the closed `icon-name` enum before this runs
// (icongen generates that list from the vendored files), so a miss here means
// the enum and the files have drifted — worth a loud panic at page-compile time,
// which happens at server start, not per request.
func iconItem(p *Element) Item {
	name, ok := Get(p, "icon")
	if !ok || name == "" {
		return nil
	}
	body, found := icons.Body(name)
	if !found {
		panic(fmt.Sprintf("kit: icon %q is in the vocabulary but not in icons/svg — re-run `go generate ./icons`", name))
	}
	return Raw(`<svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" ` +
		`stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + body + `</svg>`)
}

// withIcon puts the glyph ahead of a control's own inline content. A labelled
// control keeps its words — a menu row or a button is read by its text, and the
// glyph is an anchor for the eye, not a replacement for the label.
func withIcon(p *Element, items []Item) []Item {
	ico := iconItem(p)
	if ico == nil {
		return items
	}
	return append([]Item{ico}, items...)
}
