package xycli

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	xystrings "xy/i18nstrings"
)

// The Timeline: a Card's comments, the word-level record of its description
// edits, and the metadata trail. `comment ls` shows the discussion; --all shows
// every kind, because an agent reading a card's history wants both.

func cmdComment(a *app, args []string) error {
	return dispatch("comment", map[string]func(*app, []string) error{
		"ls": commentList, "add": commentAdd, "edit": commentEdit, "rm": commentRemove,
	}, a, args)
}

type commentRow struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Author    string `json:"author,omitempty"`
	CreatedAt string `json:"created_at"`
	ReplyTo   *int64 `json:"reply_to_id,omitempty"`
	Text      string `json:"text"`
}

func commentList(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("comment ls", s.Cli.Comment.LsUsage())
	board := a.boardFlag(fs)
	all := fs.Bool("all", false, s.Cli.Comment.AllFlag())
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
	events, err := c.Timeline(cardID)
	if err != nil {
		return err
	}
	names, err := memberNames(c, b.ID)
	if err != nil {
		return err
	}
	rows := []commentRow{}
	for _, e := range events {
		if !*all && e.Type != "comment" {
			continue
		}
		row := commentRow{ID: e.ID, Type: e.Type, CreatedAt: e.CreatedAt, ReplyTo: e.ReplyToID}
		if e.AuthorID != nil {
			row.Author = names[*e.AuthorID]
		}
		if e.Deleted {
			row.Text = s.Cli.Comment.Deleted()
		} else if plain, err := b.DK.DecField(e.PayloadEnc); err == nil {
			row.Text, _ = decodeCommentPayload(plain)
		}
		rows = append(rows, row)
	}
	return a.emit(rows, func() {
		for _, r := range rows {
			reply := ""
			if r.ReplyTo != nil {
				reply = fmt.Sprintf(" ← %d", *r.ReplyTo)
			}
			kind := ""
			if r.Type != "comment" {
				kind = " [" + r.Type + "]"
			}
			a.printf("%d  %s  %s%s%s\n", r.ID, r.CreatedAt, r.Author, kind, reply)
			for _, line := range strings.Split(strings.TrimRight(r.Text, "\n"), "\n") {
				a.printf("    %s\n", line)
			}
		}
	})
}

func commentAdd(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("comment add", s.Cli.Comment.AddUsage())
	board := a.boardFlag(fs)
	text := fs.String("text", "", s.Cli.Comment.AddTextFlag())
	file := fs.String("file", "", s.Cli.Comment.AddFileFlag())
	replyTo := fs.Int64("reply-to", 0, s.Cli.Comment.ReplyToFlag())
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
	body, err := a.readText(*text, *file)
	if err != nil {
		return err
	}
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(body) == "" {
		return errors.New("пустой комментарий")
	}
	payloadEnc, err := b.DK.EncField(body)
	if err != nil {
		return err
	}
	members, err := c.Members(b.ID)
	if err != nil {
		return err
	}
	mentions := resolveMentions(body, members)
	req := map[string]any{"payload_enc": payloadEnc, "mentions": mentions}
	if *replyTo != 0 {
		req["reply_to_id"] = *replyTo
	}
	if err := c.AddComment(cardID, req); err != nil {
		return err
	}
	return a.emit(map[string]any{"card_id": cardID, "mentions": mentions}, func() {
		a.printf("%s", s.Cli.Comment.Added(itoa(cardID)))
		if len(mentions) > 0 {
			a.printf("%s", s.Cli.Comment.Mentioned(itoa(int64(len(mentions)))))
		}
		a.printf("\n")
	})
}

func commentEdit(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("comment edit", s.Cli.Comment.EditUsage())
	board := a.boardFlag(fs)
	text := fs.String("text", "", s.Cli.Comment.EditTextFlag())
	file := fs.String("file", "", s.Cli.Comment.EditFileFlag())
	commentID, err := a.oneID(fs, args, s.Cli.Shared.WhatComment())
	if err != nil {
		return err
	}
	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	body, err := a.readText(*text, *file)
	if err != nil {
		return err
	}
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(body) == "" {
		return errors.New("пустой комментарий")
	}
	payloadEnc, err := b.DK.EncField(body)
	if err != nil {
		return err
	}
	members, err := c.Members(b.ID)
	if err != nil {
		return err
	}
	if err := c.PatchComment(commentID, map[string]any{
		"payload_enc": payloadEnc, "mentions": resolveMentions(body, members),
	}); err != nil {
		return err
	}
	return a.emit(map[string]any{"id": commentID}, func() { a.printf("%s", s.Cli.Comment.Edited(itoa(commentID))) })
}

func commentRemove(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("comment rm", s.Cli.Comment.RmUsage())
	board := a.boardFlag(fs)
	commentID, err := a.oneID(fs, args, s.Cli.Shared.WhatComment())
	if err != nil {
		return err
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	if _, _, err := a.boardRef(*board); err != nil {
		return err
	}
	if err := c.DeleteComment(commentID); err != nil {
		return err
	}
	return a.emit(map[string]any{"id": commentID}, func() { a.printf("%s", s.Cli.Comment.Removed(itoa(commentID))) })
}

var mentionRe = regexp.MustCompile(`@([A-Za-z0-9_.-]+)`)

// resolveMentions turns the @usernames of a comment into the member ids the server
// notifies by: the text is only the rendering, the ids are the routing truth
// (ADR-0009). A name that is on no member is dropped, not guessed at.
func resolveMentions(text string, members []MemberDTO) []int64 {
	byName := map[string]int64{}
	for _, m := range members {
		if m.Username != nil {
			byName[strings.ToLower(*m.Username)] = m.UserID
		}
	}
	seen := map[int64]bool{}
	var out []int64
	for _, match := range mentionRe.FindAllStringSubmatch(text, -1) {
		if id, ok := byName[strings.ToLower(match[1])]; ok && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func memberNames(c *Client, boardID int64) (map[int64]string, error) {
	members, err := c.Members(boardID)
	if err != nil {
		return nil, err
	}
	out := map[int64]string{}
	for _, m := range members {
		if m.Username != nil {
			out[m.UserID] = *m.Username
		}
	}
	return out, nil
}
