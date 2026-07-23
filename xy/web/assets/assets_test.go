package assets

import (
	"io/fs"
	"testing"
)

// TestUnderscoreFilesEmbedded guards against the `//go:embed static` footgun:
// a bare directive excludes `_`/`.`-prefixed files. The vendored noble modules
// _assert.js and _md.js must be present, or ES-module loading 404s in prod.
func TestUnderscoreFilesEmbedded(t *testing.T) {
	for _, name := range []string{
		"static/vendor/_assert.js",
		"static/vendor/_md.js",
		"static/vendor/scrypt.js",
		"static/dist/crypto.js",
		"static/dist/index.js",
	} {
		if _, err := fs.Stat(FS, name); err != nil {
			t.Errorf("not embedded: %s (%v)", name, err)
		}
	}
}
