package store

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	dopestrings "dope/i18nstrings"
)

// SlotRef is a match slot's source — the (source_type, source_ref_json) pair
// the match_slots row carries — as one value, so the scheme writes it and the
// resolver, the seed import, the rebuild check and the views read it through
// one type rather than four map walks.
type SlotRef struct {
	Type string `json:"-"` // seed · from_match · reseed · placeholder
	// seed: a basket and a number within it (Position is the legacy spelling of Number).
	Basket int `json:"basket,omitempty"`
	Number int `json:"number,omitempty"`
	// from_match: the place taken in a prior match.
	Match string `json:"match,omitempty"`
	Place int    `json:"place,omitempty"`
	// reseed: a rank in a Block's standings.
	Stage string `json:"stage,omitempty"`
	Rank  int    `json:"rank,omitempty"`
	// placeholder: free text; Label is what any kind shows instead of its default.
	Placeholder string `json:"placeholder,omitempty"`
	Label       string `json:"label,omitempty"`
}

const (
	SlotSeed        = "seed"
	SlotFromMatch   = "from_match"
	SlotReseed      = "reseed"
	SlotPlaceholder = "placeholder"
)

// SlotRefOf derives the stored ref from a scheme slot: a seed without a basket
// sits in basket 1 and, unlabelled, gets the Storage.Slot.SeedUnlabelled label.
func SlotRefOf(slot SchemeSlot) SlotRef {
	s := dopestrings.Default
	switch {
	case slot.Seed != nil:
		number := slot.Seed.Number
		if number == 0 {
			number = slot.Seed.Position
		}
		basket := slot.Seed.Basket
		if basket <= 0 {
			basket = 1
		}
		label := slot.Label
		if label == "" && slot.Seed.Basket <= 0 {
			label = s.Storage.Slot.SeedUnlabelled(strconv.Itoa(number))
		}
		return SlotRef{Type: SlotSeed, Basket: basket, Number: number, Label: label}
	case slot.FromMatch != nil:
		return SlotRef{Type: SlotFromMatch, Match: slot.FromMatch.Match, Place: slot.FromMatch.Place, Label: slot.Label}
	case slot.Reseed != nil:
		return SlotRef{Type: SlotReseed, Stage: slot.Reseed.Stage, Rank: slot.Reseed.Rank, Label: slot.Label}
	default:
		return SlotRef{Type: SlotPlaceholder, Placeholder: slot.Placeholder, Label: slot.Label}
	}
}

// SeedRef is the ref of a seed slot by number, as a flat Game seats its entrants.
func SeedRef(number int) SlotRef { return SlotRef{Type: SlotSeed, Basket: 1, Number: number} }

// ParseSlotRef reads a stored pair. A legacy seed ref may carry "position"
// for the number; a seed with no basket reads as basket 1.
func ParseSlotRef(sourceType, refJSON string) SlotRef {
	var raw struct {
		SlotRef
		Position int `json:"position"`
	}
	_ = json.Unmarshal([]byte(refJSON), &raw)
	ref := raw.SlotRef
	ref.Type = sourceType
	if ref.Type == SlotSeed {
		if ref.Number == 0 {
			ref.Number = raw.Position
		}
		if ref.Basket <= 0 {
			ref.Basket = 1
		}
	}
	return ref
}

// JSON is the stored source_ref_json: the same keys the rows have always had,
// so a row written before this type reads back unchanged.
func (r SlotRef) JSON() string {
	var v any
	switch r.Type {
	case SlotSeed:
		v = map[string]any{"basket": r.Basket, "number": r.Number, "label": r.Label}
	case SlotFromMatch:
		v = map[string]any{"match": r.Match, "place": r.Place, "label": r.Label}
	case SlotReseed:
		v = map[string]any{"stage": r.Stage, "rank": r.Rank, "label": r.Label}
	default:
		m := map[string]string{}
		if r.Placeholder != "" {
			m["placeholder"] = r.Placeholder
		}
		if r.Label != "" || r.Placeholder != "" {
			m["label"] = r.Label
		}
		v = m
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Identity is the ref without its label: what a rebuild compares to decide
// whether a match's seats still mean what the scheme says.
func (r SlotRef) Identity() string {
	switch r.Type {
	case SlotSeed:
		return fmt.Sprintf("seed:%d:%d", r.Basket, r.Number)
	case SlotFromMatch:
		return fmt.Sprintf("from_match:%s:%d", r.Match, r.Place)
	case SlotReseed:
		return fmt.Sprintf("reseed:%s:%d", r.Stage, r.Rank)
	}
	return r.Type
}

// DisplayLabel is the human label of a slot whose seat is empty: its Label if
// any (legacy schemes baked the English "seed-N"; it reads as the
// Storage.Slot.SeedLegacyPrefix label), else the kind's default.
func (r SlotRef) DisplayLabel() string {
	s := dopestrings.Default
	if r.Label != "" {
		if rest, found := strings.CutPrefix(r.Label, "seed-"); found {
			return s.Storage.Slot.SeedLegacyPrefix() + rest
		}
		return r.Label
	}
	switch r.Type {
	case SlotSeed:
		return s.Storage.Slot.SeedBasket(strconv.Itoa(r.Basket), strconv.Itoa(r.Number))
	case SlotFromMatch:
		return fmt.Sprintf("%s%d", r.Match, r.Place)
	case SlotReseed:
		return s.Storage.Slot.Reseed(strconv.Itoa(r.Rank))
	case SlotPlaceholder:
		if r.Placeholder != "" {
			return r.Placeholder
		}
	}
	return s.Storage.Slot.Placeholder()
}
