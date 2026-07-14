// Package assets embeds the frontend static files (JS/CSS/HTML/fonts) served by
// the web layer. The embedded FS keeps the leading "static/" path segment so the
// HTTP file server and ETag map can address files exactly as they appear on disk.
package assets

import "embed"

//go:embed static ui
var FS embed.FS
