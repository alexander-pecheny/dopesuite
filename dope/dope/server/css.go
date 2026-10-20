package dopeserver

import (
	"code.pecheny.me/pecheny/dopesuite/dopecore/webassets"
	kit "pecheny.me/dopeuikit/kit"

	"dope/dope/web/assets"
)

// newAssets resolves dope's asset source (live disk in dev, else the embedded
// FS) with the kit's files wired in: ETags, the core+dope stylesheet, the fonts.
func newAssets() *webassets.Assets { return kit.Assets(assets.FS, ".", "dope/web/assets") }
