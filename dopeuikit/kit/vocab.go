package kit

//go:generate go run ../cmd/uigen -core vocab.json -pkg kit -base pecheny.me/dopeuikit/ui -out tags_gen.go

import (
	_ "embed"

	base "pecheny.me/dopeuikit/ui"
)

//go:embed vocab.json
var coreVocabJSON []byte

// CoreVocab is the shared design system's vocabulary, embedded from vocab.json.
// App overlays merge over it (adding only); cmd/uigen reads the on-disk copy.
var CoreVocab = base.MustLoadVocab(coreVocabJSON)
