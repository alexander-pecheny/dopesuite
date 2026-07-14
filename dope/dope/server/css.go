package dopeserver

import (
	"pecheny.me/dopecore/webassets"
	kit "pecheny.me/dopeuikit/kit"

	"dope/dope/web/assets"
)

// newAssets resolves dope's asset source (live disk in dev, else the embedded
// FS) and everything derived from it: ETags, the core+dope stylesheet, the
// fonts.
func newAssets() *webassets.Assets {
	return webassets.New(webassets.Config{
		Embedded:        assets.FS,
		DiskRoots:       []string{".", "dope/web/assets"},
		CoreCSS:         kit.CoreCSS,
		CoreCSSDiskPath: "../dopeuikit/assets/core.css",
		Fonts:           kit.Fonts,
		FontsDiskRoot:   "../dopeuikit/assets",
	})
}
