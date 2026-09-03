package xycli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	xystrings "xy/i18nstrings"
)

// Board is a snapshot decrypted under a held key — the plaintext model every
// command works on. Nothing here is ever written back as it stands: a write
// re-encrypts the one field it changes.
type Board struct {
	ID         int64
	Name       string
	Role       string
	DK         DataKey
	Lists      []List
	Groups     map[int64]string
	Cards      []Card
	Labels     []Label
	CardLabels []cardLabelDTO
}

type List struct {
	ID      int64
	Title   string
	Rank    string
	Type    string
	GroupID *int64
}

type Card struct {
	ID        int64
	ListID    int64
	Kind      string
	Desc      string
	Alias     string
	Rank      string
	CreatedAt string
}

type Label struct {
	ID    int64
	Name  string
	Color string
}

// LoadBoard fetches a board's snapshot and decrypts every field of it.
func LoadBoard(c *Client, dk DataKey, boardID int64) (*Board, error) {
	snap, err := c.Snapshot(boardID)
	if err != nil {
		return nil, err
	}
	b := &Board{ID: snap.ID, Name: snap.Name, Role: snap.Role, DK: dk,
		Groups: map[int64]string{}, CardLabels: snap.CardLabels}
	for _, g := range snap.Groups {
		name, err := dk.DecField(g.NameEnc)
		if err != nil {
			return nil, fmt.Errorf("группа %d: %w", g.ID, err)
		}
		b.Groups[g.ID] = name
	}
	for _, l := range snap.Lists {
		title, err := dk.DecField(l.TitleEnc)
		if err != nil {
			return nil, fmt.Errorf("список %d: %w", l.ID, err)
		}
		b.Lists = append(b.Lists, List{ID: l.ID, Title: title, Rank: l.Rank, Type: l.Type, GroupID: l.GroupID})
	}
	for _, c := range snap.Cards {
		desc, err := dk.DecField(c.DescEnc)
		if err != nil {
			return nil, fmt.Errorf("карточка %d: %w", c.ID, err)
		}
		card := Card{ID: c.ID, ListID: c.ListID, Kind: c.Kind, Desc: desc, Rank: c.Rank, CreatedAt: c.CreatedAt}
		if c.AliasEnc != nil {
			if card.Alias, err = dk.DecField(*c.AliasEnc); err != nil {
				return nil, fmt.Errorf("карточка %d (алиас): %w", c.ID, err)
			}
		}
		b.Cards = append(b.Cards, card)
	}
	for _, l := range snap.Labels {
		name, err := dk.DecField(l.NameEnc)
		if err != nil {
			return nil, fmt.Errorf("метка %d: %w", l.ID, err)
		}
		color, err := dk.DecField(l.ColorEnc)
		if err != nil {
			return nil, fmt.Errorf("метка %d (цвет): %w", l.ID, err)
		}
		b.Labels = append(b.Labels, Label{ID: l.ID, Name: name, Color: color})
	}
	sort.Slice(b.Lists, func(i, j int) bool { return b.Lists[i].Rank < b.Lists[j].Rank })
	sort.Slice(b.Cards, func(i, j int) bool { return b.Cards[i].Rank < b.Cards[j].Rank })
	return b, nil
}

// CardsOf returns one list's cards in board order.
func (b *Board) CardsOf(listID int64) []Card {
	var out []Card
	for _, c := range b.Cards {
		if c.ListID == listID {
			out = append(out, c)
		}
	}
	return out
}

// ListScope is what a per-list command works on: the list, or its whole group
// (cards concatenated in board order), which is how a tour is exported.
func (b *Board) ListScope(listID int64) (title string, cards []Card, err error) {
	l, err := b.List(listID)
	if err != nil {
		return "", nil, err
	}
	if l.GroupID == nil {
		return l.Title, b.CardsOf(l.ID), nil
	}
	for _, member := range b.Lists {
		if member.GroupID != nil && *member.GroupID == *l.GroupID {
			cards = append(cards, b.CardsOf(member.ID)...)
		}
	}
	return b.Groups[*l.GroupID], cards, nil
}

func (b *Board) List(id int64) (List, error) {
	for _, l := range b.Lists {
		if l.ID == id {
			return l, nil
		}
	}
	return List{}, fmt.Errorf("список %d не найден на доске", id)
}

func (b *Board) Card(id int64) (Card, error) {
	for _, c := range b.Cards {
		if c.ID == id {
			return c, nil
		}
	}
	return Card{}, fmt.Errorf("карточка %d не найдена на доске", id)
}

// Label resolves a label by id or by name, the forgiving way a search matches.
func (b *Board) Label(ref string) (Label, error) {
	return pickOne(b.Labels, ref, xystrings.Default.Cli.Shared.WhatLabel(),
		func(l Label) int64 { return l.ID }, func(l Label) string { return l.Name })
}

// Title is how a card shows in a listing: its Alias, else the first line of its
// 4s with the marker stripped.
func (c Card) Title() string {
	if c.Alias != "" {
		return c.Alias
	}
	for _, line := range strings.Split(c.Desc, "\n") {
		if _, isVersion := versionLineName(line); isVersion {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if _, rest, ok := matchMarker(line); ok {
			if rest != "" {
				return rest
			}
			continue
		}
		return strings.TrimSpace(line)
	}
	return ""
}

// ---- comment payloads ----

// A comment's payload is a plain string until it carries images; then it is
// {"xy":1,"t":text,"img":[…]}. The marker is what keeps a hand-typed JSON
// comment from being mistaken for the envelope (web/ts/commentpayload.ts).
type commentPayload struct {
	XY  int     `json:"xy"`
	T   string  `json:"t"`
	Img []int64 `json:"img"`
}

func decodeCommentPayload(raw string) (text string, images []int64) {
	if strings.HasPrefix(raw, "{") {
		var p commentPayload
		if err := json.Unmarshal([]byte(raw), &p); err == nil && p.XY == 1 {
			return p.T, p.Img
		}
	}
	return raw, nil
}
