package kit

import "pecheny.me/dopeuikit/assets"

// The kit is the API for the shared static resources; the raw embed lives in the
// assets package. Apps concatenate CoreCSS ahead of their own layer and overlay
// the fonts at /static/fonts/.
var (
	// CoreCSS is the shared design-system stylesheet (core.css).
	CoreCSS = assets.CoreCSS
	// LoginJS is the shared login page script, served at /static/login.js.
	LoginJS = assets.LoginJS
	// MenuJS is the site-wide chrome script (theme boot + ☰ menu), /static/menu.js.
	MenuJS = assets.MenuJS
	// Fonts is the font directory (the variable noto-sans-*/jetbrains-mono-* woff2),
	// served at /static/fonts/.
	Fonts = assets.Fonts
)
