// Package web is chgksuite's browser half: the 4s parser and the question
// renderer in ts/, which xy and qy build from source, and the renderer's
// stylesheet, which their servers serve.
package web

import _ "embed"

// PreviewCSS is the question renderer's stylesheet (preview.css).
//
//go:embed preview.css
var PreviewCSS []byte

// PreviewCSSDiskPath is preview.css as a dev server finds it: both apps run
// from their module directory, a sibling of chgksuite/.
const PreviewCSSDiskPath = "../chgksuite/web/preview.css"
