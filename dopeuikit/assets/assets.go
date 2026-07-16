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

// LoginJS is the shared multi-step login page script (username → password/code
// branch). Both apps' login.dopeui pages load it as /static/login.js; the
// post-login destination comes from a data-login-redirect attribute on the page
// (default "/").
//
//go:embed login.js
var LoginJS []byte

// Fonts is the font directory (the variable noto-sans-*/jetbrains-mono-* woff2),
// served at /static/fonts/.
//
//go:embed fonts
var Fonts embed.FS
