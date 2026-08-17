package resolver

import "testing"

// A stage's config is stored inside storeutil.StageConfigJSON's envelope; a
// Kind whose config sits at the envelope's top level (a reseed's sort) reads
// the envelope itself.
func TestKindConfigReadsBothStoredShapes(t *testing.T) {
	for raw, want := range map[string]string{
		`{"config":{"lives":2},"sources":["s1"]}`:    `{"lives":2}`,
		`{"sort":[{"metric":"total","dir":"desc"}]}`: `{"sort":[{"metric":"total","dir":"desc"}]}`,
		`{"config":null,"teams":[]}`:                 `{"config":null,"teams":[]}`,
		``:                                           `{}`,
	} {
		if got := string(KindConfig([]byte(raw))); got != want {
			t.Errorf("KindConfig(%s) = %s, want %s", raw, got, want)
		}
	}
}
