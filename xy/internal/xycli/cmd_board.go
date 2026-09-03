package xycli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	xystrings "xy/i18nstrings"

	"xy/internal/rank"
)

// ---- board show ----

func cmdBoard(a *app, args []string) error {
	if len(args) == 0 || args[0] != "show" {
		return errors.New("пока есть только `xy-cli board show --board <id|имя>`")
	}
	s := xystrings.Default
	fs := a.flags("board show", s.Cli.Board.Usage())
	board := a.boardFlag(fs)
	_, err := a.parse(fs, args[1:])
	if err != nil {
		return err
	}
	_, b, err := a.open(*board)
	if err != nil {
		return err
	}
	type cardRow struct {
		ID    int64  `json:"id"`
		Kind  string `json:"kind"`
		Title string `json:"title"`
	}
	type listRow struct {
		ID    int64     `json:"id"`
		Title string    `json:"title"`
		Group string    `json:"group,omitempty"`
		Cards []cardRow `json:"cards"`
	}
	out := struct {
		ID    int64     `json:"id"`
		Name  string    `json:"name"`
		Role  string    `json:"role"`
		Lists []listRow `json:"lists"`
	}{ID: b.ID, Name: b.Name, Role: b.Role}
	for _, l := range b.Lists {
		row := listRow{ID: l.ID, Title: l.Title, Cards: []cardRow{}}
		if l.GroupID != nil {
			row.Group = b.Groups[*l.GroupID]
		}
		for _, c := range b.CardsOf(l.ID) {
			row.Cards = append(row.Cards, cardRow{ID: c.ID, Kind: c.Kind, Title: c.Title()})
		}
		out.Lists = append(out.Lists, row)
	}
	return a.emit(out, func() {
		a.printf("#%d %s (%s)\n", out.ID, out.Name, out.Role)
		for _, l := range out.Lists {
			group := ""
			if l.Group != "" {
				group = "  🔗 " + l.Group
			}
			a.printf("\n [%d] %s%s\n", l.ID, l.Title, group)
			for _, c := range l.Cards {
				kind := ""
				if c.Kind != "normal" && c.Kind != "question" {
					kind = " (" + c.Kind + ")"
				}
				a.printf("   %6d  %s%s\n", c.ID, oneLine(c.Title), kind)
			}
		}
	})
}

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len([]rune(s)) > 90 {
		return string([]rune(s)[:89]) + "…"
	}
	return s
}

// ---- lists ----

func cmdList(a *app, args []string) error {
	return dispatch("list", map[string]func(*app, []string) error{
		"add": listAdd, "rename": listRename, "rm": listRemove,
	}, a, args)
}

func listAdd(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("list add", s.Cli.List.AddUsage())
	board := a.boardFlag(fs)
	title := fs.String("title", "", s.Cli.List.AddTitleFlag())
	after := fs.Int64("after", 0, s.Cli.List.AddAfterFlag())
	_, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if *title == "" {
		return errors.New("нужен --title")
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	ranks := make([]string, len(b.Lists))
	ids := make([]int64, len(b.Lists))
	for i, l := range b.Lists {
		ranks[i], ids[i] = l.Rank, l.ID
	}
	newRank, err := rankFor(ranks, ids, *after, 0)
	if err != nil {
		return err
	}
	titleEnc, err := b.DK.EncField(*title)
	if err != nil {
		return err
	}
	id, err := c.CreateList(b.ID, titleEnc, newRank)
	if err != nil {
		return err
	}
	return a.emit(map[string]any{"id": id}, func() { a.printf("%s", s.Cli.List.Created(itoa(id), *title)) })
}

func listRename(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("list rename", s.Cli.List.RenameUsage())
	board := a.boardFlag(fs)
	title := fs.String("title", "", s.Cli.List.RenameTitleFlag())
	rest, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || *title == "" {
		return errors.New("нужен id списка и --title")
	}
	listID, err := parseID(rest[0], s.Cli.Shared.WhatList())
	if err != nil {
		return err
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	if _, err := b.List(listID); err != nil {
		return err
	}
	titleEnc, err := b.DK.EncField(*title)
	if err != nil {
		return err
	}
	if err := c.PatchList(listID, map[string]any{"title_enc": titleEnc}); err != nil {
		return err
	}
	return a.emit(map[string]any{"id": listID}, func() { a.printf("%s", s.Cli.List.Renamed(itoa(listID))) })
}

func listRemove(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("list rm", s.Cli.List.RmUsage())
	board := a.boardFlag(fs)
	listID, err := a.oneID(fs, args, s.Cli.Shared.WhatList())
	if err != nil {
		return err
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	l, err := b.List(listID)
	if err != nil {
		return err
	}
	if err := c.DeleteList(listID); err != nil {
		return err
	}
	return a.emit(map[string]any{"id": listID}, func() { a.printf("%s", s.Cli.List.Removed(itoa(listID), l.Title)) })
}

// ---- cards ----

func cmdCard(a *app, args []string) error {
	return dispatch("card", map[string]func(*app, []string) error{
		"get": cardGet, "set": cardSet, "add": cardAdd, "mv": cardMove, "rm": cardRemove,
	}, a, args)
}

// descHash is what --expect compares: a short digest of the 4s a command read,
// so a write refuses to clobber an edit made in between.
func descHash(desc string) string {
	sum := sha256.Sum256([]byte(desc))
	return hex.EncodeToString(sum[:])[:12]
}

func cardGet(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("card get", s.Cli.Card.GetUsage())
	board := a.boardFlag(fs)
	cardID, err := a.oneID(fs, args, s.Cli.Shared.WhatCard())
	if err != nil {
		return err
	}
	_, b, err := a.open(*board)
	if err != nil {
		return err
	}
	card, err := b.Card(cardID)
	if err != nil {
		return err
	}
	return a.emit(map[string]any{
		"id": card.ID, "list_id": card.ListID, "kind": card.Kind,
		"alias": card.Alias, "desc": card.Desc, "hash": descHash(card.Desc),
	}, func() {
		a.printf("%s", card.Desc)
		if !strings.HasSuffix(card.Desc, "\n") {
			a.printf("\n")
		}
		a.note("%s", s.Cli.Card.GetHeader(itoa(card.ID), itoa(card.ListID), descHash(card.Desc)))
	})
}

func cardSet(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("card set", s.Cli.Card.SetUsage())
	board := a.boardFlag(fs)
	text := fs.String("text", "", s.Cli.Card.SetTextFlag())
	file := fs.String("file", "", s.Cli.Card.FileFlag())
	expect := fs.String("expect", "", s.Cli.Card.ExpectFlag())
	alias := fs.String("alias", "", s.Cli.Card.AliasFlag())
	kind := fs.String("kind", "", s.Cli.Card.SetKindFlag())
	cardID, err := a.oneID(fs, args, s.Cli.Shared.WhatCard())
	if err != nil {
		return err
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	card, err := b.Card(cardID)
	if err != nil {
		return err
	}
	if *expect != "" && descHash(card.Desc) != *expect {
		return fmt.Errorf("карточка %d изменилась с момента чтения (сейчас %s, ожидалось %s)", cardID, descHash(card.Desc), *expect)
	}
	body := map[string]any{}
	desc := card.Desc
	// Content comes from stdin (or --text/--file); a metadata-only edit passes
	// --alias/--kind alone and leaves the 4s untouched.
	if *text != "" || *file != "" || (*alias == "" && *kind == "") {
		if desc, err = a.readText(*text, *file); err != nil {
			return err
		}
		if strings.TrimSpace(desc) == "" {
			return errors.New("пустой 4s: карточка не стирается молча — дайте текст на stdin, --text или --file")
		}
		if desc != card.Desc {
			descEnc, err := b.DK.EncField(desc)
			if err != nil {
				return err
			}
			// The Timeline's memory of what the question used to say — written on a
			// real change only, as the browser's rewrites do. The server writes the
			// row, but only from a payload the client seals: before/after are
			// plaintext it never sees.
			event, err := json.Marshal(map[string]string{"before": card.Desc, "after": desc})
			if err != nil {
				return err
			}
			eventEnc, err := b.DK.EncField(string(event))
			if err != nil {
				return err
			}
			body["description_enc"] = descEnc
			body["desc_event_enc"] = eventEnc
		}
	}
	if *alias != "" {
		aliasEnc, err := b.DK.EncField(*alias)
		if err != nil {
			return err
		}
		body["alias_enc"] = aliasEnc
	}
	if *kind != "" {
		body["kind"] = *kind
	}
	if len(body) == 0 {
		return a.emit(map[string]any{"id": cardID, "hash": descHash(desc), "changed": false}, func() {
			a.printf("%s", s.Cli.Card.Unchanged(itoa(cardID)))
		})
	}
	if err := c.PatchCard(cardID, body); err != nil {
		return err
	}
	return a.emit(map[string]any{"id": cardID, "hash": descHash(desc)}, func() {
		a.printf("%s", s.Cli.Card.Updated(itoa(cardID), descHash(desc)))
	})
}

func cardAdd(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("card add", s.Cli.Card.AddUsage())
	board := a.boardFlag(fs)
	list := fs.Int64("list", 0, s.Cli.Card.AddListFlag())
	text := fs.String("text", "", s.Cli.Card.AddTextFlag())
	file := fs.String("file", "", s.Cli.Card.FileFlag())
	kind := fs.String("kind", "normal", s.Cli.Card.AddKindFlag())
	alias := fs.String("alias", "", s.Cli.Card.AliasFlag())
	after := fs.Int64("after", 0, s.Cli.Card.AfterFlag())
	before := fs.Int64("before", 0, s.Cli.Card.BeforeFlag())
	_, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if *list == 0 {
		return errors.New("нужен --list")
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	if _, err := b.List(*list); err != nil {
		return err
	}
	desc, err := a.readText(*text, *file)
	if err != nil {
		return err
	}
	if strings.TrimSpace(desc) == "" {
		return errors.New("пустая карточка: дайте 4s на stdin или --text")
	}
	cards := b.CardsOf(*list)
	ranks := make([]string, len(cards))
	ids := make([]int64, len(cards))
	for i, card := range cards {
		ranks[i], ids[i] = card.Rank, card.ID
	}
	newRank, err := rankFor(ranks, ids, *after, *before)
	if err != nil {
		return err
	}
	descEnc, err := b.DK.EncField(desc)
	if err != nil {
		return err
	}
	body := map[string]any{"description_enc": descEnc, "rank": newRank, "kind": *kind}
	if *alias != "" {
		aliasEnc, err := b.DK.EncField(*alias)
		if err != nil {
			return err
		}
		body["alias_enc"] = aliasEnc
	}
	id, err := c.CreateCard(*list, body)
	if err != nil {
		return err
	}
	return a.emit(map[string]any{"id": id, "rank": newRank}, func() { a.printf("%s", s.Cli.Card.Created(itoa(id))) })
}

func cardMove(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("card mv", "xy-cli card mv <id> --board B [--list L] [--after id|--before id]")
	board := a.boardFlag(fs)
	list := fs.Int64("list", 0, s.Cli.Card.MvListFlag())
	after := fs.Int64("after", 0, s.Cli.Card.AfterFlag())
	before := fs.Int64("before", 0, s.Cli.Card.BeforeFlag())
	cardID, err := a.oneID(fs, args, s.Cli.Shared.WhatCard())
	if err != nil {
		return err
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	card, err := b.Card(cardID)
	if err != nil {
		return err
	}
	target := card.ListID
	if *list != 0 {
		if _, err := b.List(*list); err != nil {
			return err
		}
		target = *list
	}
	var ranks []string
	var ids []int64
	for _, other := range b.CardsOf(target) {
		if other.ID == cardID {
			continue
		}
		ranks = append(ranks, other.Rank)
		ids = append(ids, other.ID)
	}
	newRank, err := rankFor(ranks, ids, *after, *before)
	if err != nil {
		return err
	}
	body := map[string]any{"rank": newRank}
	if target != card.ListID {
		body["list_id"] = target
	}
	if err := c.PatchCard(cardID, body); err != nil {
		return err
	}
	return a.emit(map[string]any{"id": cardID, "list_id": target, "rank": newRank}, func() {
		a.printf("%s", s.Cli.Card.Moved(itoa(cardID), itoa(target)))
	})
}

func cardRemove(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("card rm", s.Cli.Card.RmUsage())
	board := a.boardFlag(fs)
	cardID, err := a.oneID(fs, args, s.Cli.Shared.WhatCard())
	if err != nil {
		return err
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	if _, err := b.Card(cardID); err != nil {
		return err
	}
	if err := c.DeleteCard(cardID); err != nil {
		return err
	}
	return a.emit(map[string]any{"id": cardID}, func() { a.printf("%s", s.Cli.Card.Removed(itoa(cardID))) })
}

// rankFor places an item among ranks (ordered, parallel to ids): after/before a
// named neighbour, or at the end when neither is given.
func rankFor(ranks []string, ids []int64, after, before int64) (string, error) {
	at := func(id int64) int {
		for i, other := range ids {
			if other == id {
				return i
			}
		}
		return -1
	}
	switch {
	case after != 0:
		i := at(after)
		if i < 0 {
			return "", fmt.Errorf("после чего вставлять: %d здесь нет", after)
		}
		next := ""
		if i+1 < len(ranks) {
			next = ranks[i+1]
		}
		return rank.Between(ranks[i], next)
	case before != 0:
		i := at(before)
		if i < 0 {
			return "", fmt.Errorf("перед чем вставлять: %d здесь нет", before)
		}
		prev := ""
		if i > 0 {
			prev = ranks[i-1]
		}
		return rank.Between(prev, ranks[i])
	}
	last := ""
	if len(ranks) > 0 {
		last = ranks[len(ranks)-1]
	}
	return rank.After(last)
}
