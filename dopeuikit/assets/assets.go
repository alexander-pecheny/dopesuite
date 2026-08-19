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
// branch). The page it drives is LoginPage, which loads it as /static/login.js;
// the post-login destination comes from the page's data-login-redirect attribute
// (default "/"). Built from ts/login.ts by `just build-web uikit` (root
// ADR-0001) — dist/ is gitignored, so build before any go build of this module.
//
//go:embed dist/login.js
var LoginJS []byte

// LoginPage is the login page's .dopeui source with {{title}} and {{redirect}}
// holes — kit.LoginPage fills them. It lives beside login.ts because the script
// hard-requires the page's ids.
//
//go:embed ui/login.dopeui
var LoginPage []byte

// MenuJS is the site-wide chrome script (theme boot + ☰ menu + Appearance
// modal), served at /static/menu.js on every page. App-specific labels come
// from window.dopeMenuConfig, set by an app boot script loaded before it.
// Built from ts/menu.ts (same build as LoginJS).
//
//go:embed dist/menu.js
var MenuJS []byte

// Fonts is the font directory (the variable noto-sans-*/jetbrains-mono-* woff2),
// served at /static/fonts/.
//
//go:embed fonts
var Fonts embed.FS
