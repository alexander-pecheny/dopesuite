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

// EnumSpec is one entry of vocab.json's "enums": a token set plus the Go const
// prefix cmd/uigen uses to name the generated value constants.
type EnumSpec struct {
	Prefix string   `json:"prefix"`
	Values []string `json:"values"`
}

// PropSpec is one prop of a primitive (or a universal prop). Kind is "" for a
// string-valued prop or "bare" for a boolean; Enum names an EnumSpec when the
// value is restricted to a token set.
type PropSpec struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Enum     string `json:"enum"`
	Required bool   `json:"required"`
}

// PrimitiveSpec is one entry of vocab.json's "primitives". Children is decoded
// separately (string policy or named-set) into ChildPolicy.
type PrimitiveSpec struct {
	Name  string     `json:"name"`
	Props []PropSpec `json:"props"`

	ChildPolicy string          // "any" | "text" | "none" | "named"
	ChildSet    map[string]bool // populated when ChildPolicy == "named"

	propByName map[string]PropSpec
}

func (p PrimitiveSpec) prop(name string) (PropSpec, bool) {
	s, ok := p.propByName[name]
	return s, ok
}

type rawPrimitive struct {
	Name     string          `json:"name"`
	Props    []PropSpec      `json:"props"`
	Children json.RawMessage `json:"children"`
}

type rawVocab struct {
	Enums        map[string]EnumSpec `json:"enums"`
	Universal    []PropSpec          `json:"universal"`
	PropPatterns []string            `json:"propPatterns"`
	Primitives   []rawPrimitive      `json:"primitives"`
}

// Vocab is the loaded, indexed v2 primitive vocabulary consulted by the
// validator and cmd/uigen. render.go owns the HTML expansion and does not read
// it.
type Vocab struct {
	Enums        map[string]EnumSpec
	Universal    []PropSpec
	PropPatterns []string // prefixes, e.g. "aria-", "data-" (trailing "*" stripped)
	Primitives   []PrimitiveSpec

	primByName    map[string]PrimitiveSpec
	universalByNm map[string]PropSpec
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

// LoadVocab parses a vocab.json document. Exported for cmd/uigen, which reads
// an on-disk copy rather than the embedded one.
func LoadVocab(data []byte) (*Vocab, error) {
	var raw rawVocab
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("ui: parse vocab.json: %w", err)
	}
	v := &Vocab{
		Enums:         raw.Enums,
		Universal:     raw.Universal,
		primByName:    make(map[string]PrimitiveSpec, len(raw.Primitives)),
		universalByNm: make(map[string]PropSpec, len(raw.Universal)),
	}
	for _, p := range raw.PropPatterns {
		v.PropPatterns = append(v.PropPatterns, strings.TrimSuffix(p, "*"))
	}
	for _, u := range raw.Universal {
		v.universalByNm[u.Name] = u
	}
	for _, rp := range raw.Primitives {
		ps := PrimitiveSpec{Name: rp.Name, Props: rp.Props, propByName: make(map[string]PropSpec, len(rp.Props))}
		for _, pr := range rp.Props {
			ps.propByName[pr.Name] = pr
		}
		if err := decodeChildren(&ps, rp.Children); err != nil {
			return nil, fmt.Errorf("ui: primitive %q: %w", rp.Name, err)
		}
		v.Primitives = append(v.Primitives, ps)
		v.primByName[rp.Name] = ps
	}
	return v, nil
}

func decodeChildren(ps *PrimitiveSpec, raw json.RawMessage) error {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "any", "text", "none":
			ps.ChildPolicy = s
			return nil
		default:
			return fmt.Errorf("unknown children policy %q", s)
		}
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return fmt.Errorf("children must be a policy string or a name list")
	}
	ps.ChildPolicy = "named"
	ps.ChildSet = make(map[string]bool, len(names))
	for _, n := range names {
		ps.ChildSet[n] = true
	}
	return nil
}

// Primitive returns the spec for a primitive name.
func (v *Vocab) Primitive(name string) (PrimitiveSpec, bool) {
	s, ok := v.primByName[name]
	return s, ok
}

// PropFor resolves prop name on primitive tag: the primitive's own prop wins,
// then a universal prop. ok is false when the name matches neither (patterns
// are handled by the validator, not here).
func (v *Vocab) PropFor(tag, name string) (PropSpec, bool) {
	if p, ok := v.primByName[tag]; ok {
		if s, ok := p.prop(name); ok {
			return s, true
		}
	}
	s, ok := v.universalByNm[name]
	return s, ok
}

// PatternProp reports whether name matches a propPattern (aria-*, data-*).
func (v *Vocab) PatternProp(name string) bool {
	for _, prefix := range v.PropPatterns {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// EnumHas reports whether value is a member of the named enum.
func (v *Vocab) EnumHas(enum, value string) bool {
	e, ok := v.Enums[enum]
	if !ok {
		return false
	}
	for _, tok := range e.Values {
		if tok == value {
			return true
		}
	}
	return false
}
