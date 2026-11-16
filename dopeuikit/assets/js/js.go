// Package js embeds DopeUIKit's built scripts, login.js and menu.js. They are
// built from ../ts by `just build-web uikit` (root ADR-0001) into dist/, which
// is gitignored, so build before any go build that imports this package. It is
// kept apart from package assets so that core.css and the fonts stay importable
// from a plain module fetch, without the frontend toolchain.
package js

import _ "embed"

// LoginJS is the shared multi-step login page script (username → password/code
// branch). The page it drives is assets.LoginPage, which loads it as
// /static/login.js; the post-login destination comes from the page's
// data-login-redirect attribute (default "/"). Built from ts/login.ts.
//
//go:embed dist/login.js
var LoginJS []byte

// MenuJS is the site-wide chrome script (theme boot + ☰ menu + Appearance
// modal), served at /static/menu.js on every page. App-specific labels come
// from window.dopeMenuConfig, set by an app boot script loaded before it.
// Built from ts/menu.ts (same build as LoginJS).
//
//go:embed dist/menu.js
var MenuJS []byte
