package ui

import (
	"encoding/json"
	"fmt"
	"strings"
)

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
// separately (string policy or named-set) into ChildPolicy. Inline marks an
// inline primitive (usable in text runs); Placement ("header"/"overlay"/"") is
// consulted by the page expander to route a child to the header, after </main>,
// or into main.
type PrimitiveSpec struct {
	Name      string     `json:"name"`
	Props     []PropSpec `json:"props"`
	Inline    bool       `json:"inline"`
	Placement string     `json:"placement"`

	ChildPolicy string          // "any" | "text" | "content" | "none" | "named"
	ChildSet    map[string]bool // populated when ChildPolicy == "named"

	propByName map[string]PropSpec
}

func (p PrimitiveSpec) prop(name string) (PropSpec, bool) {
	s, ok := p.propByName[name]
	return s, ok
}

type rawPrimitive struct {
	Name      string          `json:"name"`
	Props     []PropSpec      `json:"props"`
	Inline    bool            `json:"inline"`
	Placement string          `json:"placement"`
	Children  json.RawMessage `json:"children"`
}

type rawVocab struct {
	Root         string              `json:"root"`
	Enums        map[string]EnumSpec `json:"enums"`
	Universal    []PropSpec          `json:"universal"`
	PropPatterns []string            `json:"propPatterns"`
	Primitives   []rawPrimitive      `json:"primitives"`
}

// Vocab is the loaded, indexed primitive vocabulary consulted by the validator
// and cmd/uigen. Expanders own the HTML expansion and do not read it. Root names
// the single primitive a page file must be rooted at (declared by the design
// system, e.g. "page"); "" leaves the root unconstrained.
type Vocab struct {
	Root         string
	Enums        map[string]EnumSpec
	Universal    []PropSpec
	PropPatterns []string // prefixes, e.g. "aria-", "data-" (trailing "*" stripped)
	Primitives   []PrimitiveSpec

	primByName    map[string]PrimitiveSpec
	universalByNm map[string]PropSpec
}

// MustLoadVocab is LoadVocab with a panic on error — for embedded vocabularies
// loaded at init.
func MustLoadVocab(data []byte) *Vocab {
	v, err := LoadVocab(data)
	if err != nil {
		panic(err)
	}
	return v
}

// LoadVocab parses a vocab.json document. Exported for cmd/uigen (which reads an
// on-disk copy) and for the design-system layer (which embeds its own core).
func LoadVocab(data []byte) (*Vocab, error) {
	var raw rawVocab
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("ui: parse vocab.json: %w", err)
	}
	v := &Vocab{
		Root:          raw.Root,
		Enums:         map[string]EnumSpec{},
		primByName:    make(map[string]PrimitiveSpec, len(raw.Primitives)),
		universalByNm: make(map[string]PropSpec, len(raw.Universal)),
	}
	for n, e := range raw.Enums {
		v.Enums[n] = e
	}
	for _, p := range raw.PropPatterns {
		v.PropPatterns = append(v.PropPatterns, strings.TrimSuffix(p, "*"))
	}
	for _, u := range raw.Universal {
		v.Universal = append(v.Universal, u)
		v.universalByNm[u.Name] = u
	}
	for _, rp := range raw.Primitives {
		ps, err := decodePrimitive(rp)
		if err != nil {
			return nil, fmt.Errorf("ui: primitive %q: %w", rp.Name, err)
		}
		v.Primitives = append(v.Primitives, ps)
		v.primByName[rp.Name] = ps
	}
	return v, nil
}

func decodePrimitive(rp rawPrimitive) (PrimitiveSpec, error) {
	ps := PrimitiveSpec{
		Name: rp.Name, Props: rp.Props, Inline: rp.Inline, Placement: rp.Placement,
		propByName: make(map[string]PropSpec, len(rp.Props)),
	}
	for _, pr := range rp.Props {
		ps.propByName[pr.Name] = pr
	}
	return ps, decodeChildren(&ps, rp.Children)
}

// Merge returns a new Vocab that layers an app overlay over the receiver: the
// overlay may add primitives, add new enums, and add values to existing enums.
// Re-declaring a core primitive is an error (extension only).
func (v *Vocab) Merge(overlay []byte) (*Vocab, error) {
	var raw rawVocab
	if err := json.Unmarshal(overlay, &raw); err != nil {
		return nil, fmt.Errorf("ui: parse overlay vocab: %w", err)
	}
	out := &Vocab{
		Root:          v.Root,
		Enums:         map[string]EnumSpec{},
		Universal:     append([]PropSpec(nil), v.Universal...),
		PropPatterns:  append([]string(nil), v.PropPatterns...),
		Primitives:    append([]PrimitiveSpec(nil), v.Primitives...),
		primByName:    map[string]PrimitiveSpec{},
		universalByNm: map[string]PropSpec{},
	}
	for n, e := range v.Enums {
		out.Enums[n] = e
	}
	for n, p := range v.primByName {
		out.primByName[n] = p
	}
	for n, u := range v.universalByNm {
		out.universalByNm[n] = u
	}
	for n, e := range raw.Enums {
		if base, ok := out.Enums[n]; ok {
			base.Values = append(append([]string(nil), base.Values...), e.Values...)
			out.Enums[n] = base
		} else {
			out.Enums[n] = e
		}
	}
	for _, rp := range raw.Primitives {
		if _, dup := out.primByName[rp.Name]; dup {
			return nil, fmt.Errorf("ui: overlay re-declares core primitive %q (extension only)", rp.Name)
		}
		ps, err := decodePrimitive(rp)
		if err != nil {
			return nil, fmt.Errorf("ui: overlay primitive %q: %w", rp.Name, err)
		}
		out.Primitives = append(out.Primitives, ps)
		out.primByName[rp.Name] = ps
	}
	return out, nil
}

// WithExtraProps returns a copy of the vocabulary with extra props appended to
// named existing primitives — the seam for app chrome props (e.g. page `init`)
// that don't warrant re-declaring the primitive.
func (v *Vocab) WithExtraProps(extra map[string][]PropSpec) *Vocab {
	out := *v
	out.primByName = map[string]PrimitiveSpec{}
	for n, p := range v.primByName {
		out.primByName[n] = p
	}
	out.Primitives = append([]PrimitiveSpec(nil), v.Primitives...)
	for i := range out.Primitives {
		add, ok := extra[out.Primitives[i].Name]
		if !ok {
			continue
		}
		p := out.Primitives[i]
		p.Props = append(append([]PropSpec(nil), p.Props...), add...)
		p.propByName = map[string]PropSpec{}
		for _, pr := range p.Props {
			p.propByName[pr.Name] = pr
		}
		out.Primitives[i] = p
		out.primByName[p.Name] = p
	}
	return &out
}

func decodeChildren(ps *PrimitiveSpec, raw json.RawMessage) error {
	if len(raw) == 0 {
		ps.ChildPolicy = "none"
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "any", "text", "content", "none":
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
// then a universal prop. ok is false when the name matches neither.
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
