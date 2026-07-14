// Package assets embeds xy's frontend static files (JS/CSS/fonts) and the
// .dopeui page sources compiled at serve time. The embedded FS keeps the leading
// "static/"/"ui/" path segments so the HTTP file server, ETag map, and page
// compiler address files exactly as they appear on disk.
package assets

import "embed"

// Note: the `all:` prefix is required — a bare `//go:embed static` silently
// excludes files whose names begin with `_` or `.` (e.g. the vendored
// `_assert.js` / `_md.js`), which then 404 in production embed mode.
//
//go:embed all:static all:ui
var FS embed.FS
