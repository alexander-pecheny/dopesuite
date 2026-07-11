package ui

//go:generate go run ../../cmd/uigen -in vocab.json -out tags_gen.go

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed vocab.json
var vocabJSON []byte

// AttrSpec is one entry of vocab.json's "attrs" array: an attribute name and
// whether it is written bare (no value) or with a quoted value.
type AttrSpec struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "value" or "bare"
}

// ElementSpec is one entry of vocab.json's "elements" array.
type ElementSpec struct {
	Tag   string   `json:"tag"`
	Attrs []string `json:"attrs"`
	Void  bool     `json:"void"`
}

type rawVocab struct {
	GlobalAttrs  []string      `json:"globalAttrs"`
	AttrPatterns []string      `json:"attrPatterns"`
	Attrs        []AttrSpec    `json:"attrs"`
	Elements     []ElementSpec `json:"elements"`
	Classes      []string      `json:"classes"`
}

// Vocab is the loaded, indexed closed vocabulary consulted by the validator,
// the printer (void-element detection) and cmd/uigen.
type Vocab struct {
	GlobalAttrs  []string
	AttrPatterns []string // prefixes, e.g. "aria-", "data-" (the trailing "*" stripped)
	Attrs        []AttrSpec
	Elements     []ElementSpec
	Classes      []string

	globalAttrSet map[string]bool
	attrKind      map[string]string
	elementSet    map[string]ElementSpec
	classSet      map[string]bool
}

// Loaded is the vocabulary embedded from vocab.json.
var Loaded = mustLoadVocab(vocabJSON)

func mustLoadVocab(data []byte) *Vocab {
	v, err := LoadVocab(data)
	if err != nil {
		panic(err)
	}
	return v
}

// LoadVocab parses a vocab.json document. Exported for cmd/uigen, which
// generates tags_gen.go from an on-disk copy rather than the embedded one.
func LoadVocab(data []byte) (*Vocab, error) {
	var raw rawVocab
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("ui: parse vocab.json: %w", err)
	}
	v := &Vocab{
		GlobalAttrs:   raw.GlobalAttrs,
		Attrs:         raw.Attrs,
		Elements:      raw.Elements,
		Classes:       raw.Classes,
		globalAttrSet: make(map[string]bool, len(raw.GlobalAttrs)),
		attrKind:      make(map[string]string, len(raw.Attrs)),
		elementSet:    make(map[string]ElementSpec, len(raw.Elements)),
		classSet:      make(map[string]bool, len(raw.Classes)),
	}
	for _, a := range raw.GlobalAttrs {
		v.globalAttrSet[a] = true
	}
	for _, p := range raw.AttrPatterns {
		v.AttrPatterns = append(v.AttrPatterns, strings.TrimSuffix(p, "*"))
	}
	for _, a := range raw.Attrs {
		v.attrKind[a.Name] = a.Kind
	}
	for _, e := range raw.Elements {
		v.elementSet[e.Tag] = e
	}
	for _, c := range raw.Classes {
		v.classSet[c] = true
	}
	return v, nil
}

// ElementAllowed reports whether tag is in the closed vocabulary.
func (v *Vocab) ElementAllowed(tag string) bool {
	_, ok := v.elementSet[tag]
	return ok
}

// IsVoid reports whether tag is a void element (no closing tag, no
// children).
func (v *Vocab) IsVoid(tag string) bool {
	return v.elementSet[tag].Void
}

// AttrAllowed reports whether attribute name is permitted on element tag:
// a global attr, an aria-*/data-* pattern match, or listed for that element.
func (v *Vocab) AttrAllowed(tag, name string) bool {
	if v.globalAttrSet[name] {
		return true
	}
	for _, prefix := range v.AttrPatterns {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	spec, ok := v.elementSet[tag]
	if !ok {
		return false
	}
	for _, a := range spec.Attrs {
		if a == name {
			return true
		}
	}
	return false
}

// AttrKind returns "value" or "bare" for a known attribute name, or "" if
// name is only reachable via an aria-*/data-* pattern (those are always
// name+value pairs, built by the Aria/Data helpers, not per-name ctors).
func (v *Vocab) AttrKind(name string) string {
	return v.attrKind[name]
}

// ClassAllowed reports whether token is in the class-token whitelist.
func (v *Vocab) ClassAllowed(token string) bool {
	return v.classSet[token]
}
