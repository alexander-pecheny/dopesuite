// Package assets embeds DopeUIKit's shared static resources: core.css (the
// shared design system) and the variable web fonts — Noto Sans roman+italic,
// JetBrains Mono, and the five body faces a reader may switch to instead of Noto
// Sans (roman+italic each, fetched only by a browser whose reader has). Apps
// concatenate core.css ahead of their own CSS layer and overlay the fonts at
// /static/fonts/.
//
// Nothing here is build output, so a plain module fetch can import this package
// (code.pecheny.me/pecheny/dopesuite/dopeuikit/assets) for the design system
// alone. The built scripts live in package js.
package assets

import (
	"embed"
	_ "embed"
)

//go:embed core.css
var CoreCSS []byte

// LoginPage is the login page's .dopeui source with {{title}} and {{redirect}}
// holes — kit.LoginPage fills them. It lives beside login.ts because the script
// hard-requires the page's ids.
//
//go:embed ui/login.dopeui
var LoginPage []byte

// Fonts is the font directory (the variable noto-sans-*/jetbrains-mono-* woff2
// plus the alternative body faces, one file per face and style), served at
// /static/fonts/.
//
//go:embed fonts
var Fonts embed.FS
