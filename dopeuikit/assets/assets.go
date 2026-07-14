// Package assets embeds DopeUIKit's shared static resources: core.css (the
// shared design system) and the variable web fonts (Noto Sans roman+italic, JetBrains Mono). Apps concatenate core.css
// ahead of their own CSS layer and overlay the fonts at /static/fonts/.
package assets

import (
	"embed"
	_ "embed"
)

//go:embed core.css
var CoreCSS []byte

// Fonts is the font directory (the variable noto-sans-*/jetbrains-mono-* woff2),
// served at /static/fonts/.
//
//go:embed fonts
var Fonts embed.FS
