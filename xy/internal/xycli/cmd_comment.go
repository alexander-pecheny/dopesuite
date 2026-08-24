package xycli

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
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
	fs := a.flags("comment ls", "xy-cli comment ls <id карточки> --board B\nПо умолчанию — только комментарии.")
	board := a.boardFlag(fs)
	all := fs.Bool("all", false, "вся лента: правки описания и метаданные тоже")
	cardID, err := a.oneID(fs, args, "карточка")
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
			row.Text = "(удалён)"
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
	fs := a.flags("comment add", "xy-cli comment add <id карточки> --board B [--reply-to N] < текст\n@логин в тексте становится упоминанием (ADR-0009).")
	board := a.boardFlag(fs)
	text := fs.String("text", "", "текст комментария (иначе stdin)")
	file := fs.String("file", "", "файл с текстом (иначе stdin)")
	replyTo := fs.Int64("reply-to", 0, "id комментария, на который отвечаем")
	cardID, err := a.oneID(fs, args, "карточка")
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
		a.printf("комментарий добавлен к карточке %d", cardID)
		if len(mentions) > 0 {
			a.printf(" (упомянуто: %d)", len(mentions))
		}
		a.printf("\n")
	})
}

func commentEdit(a *app, args []string) error {
	fs := a.flags("comment edit", "xy-cli comment edit <id комментария> --board B < новый текст")
	board := a.boardFlag(fs)
	text := fs.String("text", "", "новый текст (иначе stdin)")
	file := fs.String("file", "", "файл с текстом (иначе stdin)")
	commentID, err := a.oneID(fs, args, "комментарий")
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
	return a.emit(map[string]any{"id": commentID}, func() { a.printf("комментарий %d изменён\n", commentID) })
}

func commentRemove(a *app, args []string) error {
	fs := a.flags("comment rm", "xy-cli comment rm <id комментария> --board B")
	board := a.boardFlag(fs)
	commentID, err := a.oneID(fs, args, "комментарий")
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
	return a.emit(map[string]any{"id": commentID}, func() { a.printf("комментарий %d удалён\n", commentID) })
}

var mentionRe = regexp.MustCompile(`@([A-Za-z0-9_.-]+)`)

// resolveMentions turns the @логины of a comment into the member ids the server
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
