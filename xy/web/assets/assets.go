// Package assets embeds xy's frontend static files (JS/CSS/HTML/fonts). The
// embedded FS keeps the leading "static/" path segment so the HTTP file server
// and ETag map address files exactly as they appear on disk.
package assets

import "embed"

// Note: the `all:` prefix is required — a bare `//go:embed static` silently
// excludes files whose names begin with `_` or `.` (e.g. the vendored
// `_assert.js` / `_md.js`), which then 404 in production embed mode.
//
//go:embed all:static
var FS embed.FS
