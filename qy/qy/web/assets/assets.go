// Package assets embeds Spliff's frontend static files and the .dopeui page
// sources the server compiles at startup. The embedded FS keeps the leading
// "static/" and "ui/" segments so the file server, the ETag map and the page
// compiler address files exactly as they appear on disk.
package assets

import "embed"

// The `all:` prefix is deliberate: a bare //go:embed silently excludes files
// whose names begin with `_` or `.`, which then 404 in production embed mode.
//
//go:embed all:static all:ui
var FS embed.FS
