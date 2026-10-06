package xycli

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The corpus jstest/eventpayload.test.js reads too: the same rows must decode
// to the same values, and the same values must encode to the same bytes, on
// both sides.
type payloadFixture struct {
	Decode []struct {
		Name string         `json:"name"`
		Kind string         `json:"kind"`
		Raw  string         `json:"raw"`
		Want map[string]any `json:"want"`
	} `json:"decode"`
	Encode []struct {
		Name string         `json:"name"`
		Kind string         `json:"kind"`
		Data map[string]any `json:"data"`
		Raw  string         `json:"raw"`
	} `json:"encode"`
}

func loadPayloadFixture(t *testing.T) payloadFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/eventpayload.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx payloadFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	return fx
}

// plainOf writes a payload in the corpus's form, the shape eventpayload.ts
// returns, so the two sides compare against one expectation.
func plainOf(kind string, p EventPayload) map[string]any {
	switch v := p.(type) {
	case CommentPayload:
		images := []any{}
		for _, id := range v.Images {
			images = append(images, float64(id))
		}
		return map[string]any{"kind": kind, "text": v.Text, "images": images}
	case DescEditPayload:
		return map[string]any{"kind": kind, "before": v.Before, "after": v.After, "author": v.Author}
	case LabelPayload:
		var id any
		if v.LabelID != nil {
			id = float64(*v.LabelID)
		}
		return map[string]any{"kind": kind, "label": v.Label, "labelId": id}
	case AttachPayload:
		return map[string]any{"kind": kind, "file": v.File}
	case ReactionPayload:
		return map[string]any{"kind": kind, "emoji": v.Emoji}
	}
	return map[string]any{"kind": "unreadable"}
}

// payloadFrom is plainOf's inverse, for the encode rows. The corpus's keys
// match the struct fields up to case, which is how encoding/json matches.
func payloadFrom(t *testing.T, kind string, data map[string]any) EventPayload {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var p EventPayload
	switch kind {
	case KindComment:
		p, err = strictDecode[CommentPayload](raw)
	case KindDescEdit:
		p, err = strictDecode[DescEditPayload](raw)
	case KindLabelAdd, KindLabelRemove:
		p, err = strictDecode[LabelPayload](raw)
	case KindAttachAdd, KindAttachRemove, KindAttachReplace:
		p, err = strictDecode[AttachPayload](raw)
	case KindReaction:
		p, err = strictDecode[ReactionPayload](raw)
	default:
		t.Fatalf("no payload for kind %q", kind)
	}
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	return p
}

func strictDecode[T EventPayload](raw []byte) (EventPayload, error) {
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(&v)
	return v, err
}

func TestDecodePayloadCorpus(t *testing.T) {
	for _, c := range loadPayloadFixture(t).Decode {
		got := plainOf(c.Kind, DecodePayload(c.Kind, c.Raw))
		if !reflect.DeepEqual(got, c.Want) {
			t.Errorf("%s: decoded %v, want %v", c.Name, got, c.Want)
		}
	}
}

func TestEncodePayloadCorpus(t *testing.T) {
	for _, c := range loadPayloadFixture(t).Encode {
		p := payloadFrom(t, c.Kind, c.Data)
		got, err := EncodePayload(c.Kind, p)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if got != c.Raw {
			t.Errorf("%s: encoded %s, want %s", c.Name, got, c.Raw)
		}
		want := map[string]any{"kind": c.Kind}
		for k, v := range c.Data {
			want[k] = v
		}
		if back := plainOf(c.Kind, DecodePayload(c.Kind, got)); !reflect.DeepEqual(back, want) {
			t.Errorf("%s: read back %v, want %v", c.Name, back, want)
		}
	}
}

func TestSealOpenPayloadPerKind(t *testing.T) {
	dk := DataKey(bytes.Repeat([]byte{7}, defaultDKLen))
	for _, c := range loadPayloadFixture(t).Encode {
		p := payloadFrom(t, c.Kind, c.Data)
		b64, err := dk.SealPayload(c.Kind, p)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if got := dk.OpenPayload(c.Kind, b64); !reflect.DeepEqual(plainOf(c.Kind, got), plainOf(c.Kind, DecodePayload(c.Kind, c.Raw))) {
			t.Errorf("%s: opened %#v", c.Name, got)
		}
	}
	other := DataKey(bytes.Repeat([]byte{8}, defaultDKLen))
	b64, err := dk.SealPayload(KindReaction, ReactionPayload{Emoji: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := other.OpenPayload(KindReaction, b64); got != (UnreadablePayload{}) {
		t.Errorf("a payload under another key opened as %#v", got)
	}
}

func TestEncodePayloadRefusesTheWrongKind(t *testing.T) {
	if _, err := EncodePayload(KindLabelAdd, AttachPayload{File: "x"}); err == nil {
		t.Error("an attachment was encoded as a label")
	}
}
