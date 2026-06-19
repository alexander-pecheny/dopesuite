// Package assets embeds xy's frontend static files (JS/CSS/HTML/fonts). The
// embedded FS keeps the leading "static/" path segment so the HTTP file server
// and ETag map address files exactly as they appear on disk.
package assets

import "embed"

//go:embed static
var FS embed.FS
