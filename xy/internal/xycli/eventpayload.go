package xycli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// The codec for a Timeline event's encrypted payload, the twin of
// web/ts/eventpayload.ts. What each kind stores:
//
//	comment    a plain string, or {"xy":1,"t":text,"img":[attachment ids]} once
//	           it carries images; the "xy" marker keeps a hand-typed JSON comment
//	           from being taken for the envelope
//	desc_edit  {"before","after"[,"author"]}; an imported edit names its author
//	label_*    {"label","label_id"}; rows from before 2026-07-26 have no label_id
//	attach_*   {"file"}
//	reaction   the emoji itself, as a raw string
//
// Both sides read testdata/eventpayload.json. A payload that will not decrypt,
// does not parse, or has a kind this codec does not know opens as
// UnreadablePayload.

// The Timeline's event kinds that carry a payload.
const (
	KindComment       = "comment"
	KindDescEdit      = "desc_edit"
	KindLabelAdd      = "label_add"
	KindLabelRemove   = "label_remove"
	KindAttachAdd     = "attach_add"
	KindAttachRemove  = "attach_remove"
	KindAttachReplace = "attach_replace"
	KindReaction      = "reaction"
)

// EventPayload is one of the *Payload types below.
type EventPayload interface{ eventPayload() }

// CommentPayload is a comment's text and the attachments it shows.
type CommentPayload struct {
	Text   string
	Images []int64
}

// DescEditPayload is a description before and after an edit. Author is empty
// unless the edit was imported and its author is not an xy user.
type DescEditPayload struct{ Before, After, Author string }

// LabelPayload names the label that came or went. LabelID is nil on old rows.
type LabelPayload struct {
	Label   string
	LabelID *int64
}

// AttachPayload names the attachment that was added, replaced or removed.
type AttachPayload struct{ File string }

// ReactionPayload is the emoji a member put on.
type ReactionPayload struct{ Emoji string }

// UnreadablePayload stands for a payload that would not open.
type UnreadablePayload struct{}

func (CommentPayload) eventPayload()    {}
func (DescEditPayload) eventPayload()   {}
func (LabelPayload) eventPayload()      {}
func (AttachPayload) eventPayload()     {}
func (ReactionPayload) eventPayload()   {}
func (UnreadablePayload) eventPayload() {}

// commentMarker is the "xy" value of a comment that carries images.
const commentMarker = 1

// The JSON shapes, fields in the order the browser writes them.
type commentJSON struct {
	XY  int     `json:"xy"`
	T   string  `json:"t"`
	Img []int64 `json:"img"`
}

type descEditJSON struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Author string `json:"author,omitempty"`
}

type labelJSON struct {
	Label   string `json:"label"`
	LabelID *int64 `json:"label_id,omitempty"`
}

type attachJSON struct {
	File string `json:"file"`
}

// marshalPlain is json.Marshal without the HTML escaping JSON.stringify does
// not do, so both sides write the same bytes.
func marshalPlain(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// EncodePayload writes the plaintext an event of the given kind stores. It
// fails when p is not the payload that kind carries.
func EncodePayload(kind string, p EventPayload) (string, error) {
	switch v := p.(type) {
	case CommentPayload:
		if kind == KindComment {
			if len(v.Images) == 0 {
				return v.Text, nil
			}
			return marshalPlain(commentJSON{XY: commentMarker, T: v.Text, Img: v.Images})
		}
	case DescEditPayload:
		if kind == KindDescEdit {
			return marshalPlain(descEditJSON(v))
		}
	case LabelPayload:
		if kind == KindLabelAdd || kind == KindLabelRemove {
			return marshalPlain(labelJSON(v))
		}
	case AttachPayload:
		if kind == KindAttachAdd || kind == KindAttachRemove || kind == KindAttachReplace {
			return marshalPlain(attachJSON(v))
		}
	case ReactionPayload:
		if kind == KindReaction {
			return v.Emoji, nil
		}
	}
	return "", fmt.Errorf("eventpayload: %T is not a %s payload", p, kind)
}

// parseObject reads a JSON object, or nil for anything else.
func parseObject(raw string) map[string]any {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

// field reads a string field: absent is "", any other type is not ok.
func field(o map[string]any, key string) (string, bool) {
	v, present := o[key]
	if !present {
		return "", true
	}
	s, ok := v.(string)
	return s, ok
}

func decodeComment(raw string) EventPayload {
	if strings.HasPrefix(raw, "{") {
		o := parseObject(raw)
		t, isText := o["t"].(string)
		img, isList := o["img"].([]any)
		if o != nil && o["xy"] == float64(commentMarker) && isText && isList {
			images := []int64{}
			for _, n := range img {
				if f, ok := n.(float64); ok {
					images = append(images, int64(f))
				}
			}
			return CommentPayload{Text: t, Images: images}
		}
	}
	return CommentPayload{Text: raw, Images: []int64{}}
}

func decodeDescEdit(raw string) EventPayload {
	o := parseObject(raw)
	if o == nil {
		return UnreadablePayload{}
	}
	before, ok1 := field(o, "before")
	after, ok2 := field(o, "after")
	author, ok3 := field(o, "author")
	if !ok1 || !ok2 || !ok3 {
		return UnreadablePayload{}
	}
	return DescEditPayload{Before: before, After: after, Author: author}
}

func decodeLabel(raw string) EventPayload {
	o := parseObject(raw)
	if o == nil {
		return UnreadablePayload{}
	}
	label, ok := field(o, "label")
	if !ok {
		return UnreadablePayload{}
	}
	p := LabelPayload{Label: label}
	switch id := o["label_id"].(type) {
	case nil:
	case float64:
		n := int64(id)
		p.LabelID = &n
	default:
		return UnreadablePayload{}
	}
	return p
}

func decodeAttach(raw string) EventPayload {
	o := parseObject(raw)
	if o == nil {
		return UnreadablePayload{}
	}
	file, ok := field(o, "file")
	if !ok {
		return UnreadablePayload{}
	}
	return AttachPayload{File: file}
}

// DecodePayload reads the plaintext of an event of the given kind.
func DecodePayload(kind, raw string) EventPayload {
	switch kind {
	case KindComment:
		return decodeComment(raw)
	case KindDescEdit:
		return decodeDescEdit(raw)
	case KindLabelAdd, KindLabelRemove:
		return decodeLabel(raw)
	case KindAttachAdd, KindAttachRemove, KindAttachReplace:
		return decodeAttach(raw)
	case KindReaction:
		if raw == "" {
			return UnreadablePayload{}
		}
		return ReactionPayload{Emoji: raw}
	}
	return UnreadablePayload{}
}

// SealPayload encodes and encrypts: what goes into payload_enc, desc_event_enc
// or event_payload_enc.
func (dk DataKey) SealPayload(kind string, p EventPayload) (string, error) {
	plain, err := EncodePayload(kind, p)
	if err != nil {
		return "", err
	}
	return dk.EncField(plain)
}

// OpenPayload decrypts and decodes; a field that will not open is unreadable.
func (dk DataKey) OpenPayload(kind, b64 string) EventPayload {
	plain, err := dk.DecField(b64)
	if err != nil {
		return UnreadablePayload{}
	}
	return DecodePayload(kind, plain)
}

// EventText is the line a listing shows for an event: a comment's words, the
// emoji, the label or file named, a description as it now reads.
func EventText(p EventPayload) string {
	switch v := p.(type) {
	case CommentPayload:
		return v.Text
	case ReactionPayload:
		return v.Emoji
	case LabelPayload:
		return v.Label
	case AttachPayload:
		return v.File
	case DescEditPayload:
		return v.After
	}
	return ""
}
