package board

import (
	"context"
	"regexp"
	"strings"
)

// upload_file: a .4s split at its blank lines, one card per question, each
// captioned by its answer.

var (
	reUploadAnswer = regexp.MustCompile(`\n! (.+?)\.?\r?\n`)
	reUploadAuthor = regexp.MustCompile(`\n@ (.+?)\r?\n`)
	reCardSplit    = regexp.MustCompile(`(\r?\n){2,}`)
)

// Cards cuts a .4s into the cards it becomes, with the caption chgksuite gives
// each: the answer, and the author too when asked.
func Cards(source string, withAuthor bool) []struct{ Caption, Text string } {
	var out []struct{ Caption, Text string }
	for _, card := range reCardSplit.Split(source, -1) {
		if card == "" || card == "\n" || card == "\r\n" {
			continue
		}
		caption := "вопрос"
		if m := reUploadAnswer.FindStringSubmatch(card); m != nil {
			caption = m[1]
			if withAuthor {
				if a := reUploadAuthor.FindStringSubmatch(card); a != nil {
					caption += " " + a[1]
				}
			}
		}
		out = append(out, struct{ Caption, Text string }{caption, card})
	}
	return out
}

// Upload posts a .4s to a list — the named one, or the board's first.
func (c *Client) Upload(ctx context.Context, b Board, source, listName string, withAuthor bool,
	say func(string),
) error {
	lists, err := c.Lists(ctx, b)
	if err != nil {
		return err
	}
	target, err := pickList(lists, listName)
	if err != nil {
		return err
	}
	say("загружаю в список «" + target.Name + "»")
	for _, card := range Cards(source, withAuthor) {
		if err := c.PostCard(ctx, b, target.ID, card.Caption, card.Text); err != nil {
			return err
		}
		say("отправлено: " + card.Caption)
	}
	return nil
}

func pickList(lists []List, name string) (List, error) {
	if name == "" {
		if len(lists) == 0 {
			return List{}, errNoLists
		}
		return lists[0], nil
	}
	for _, l := range lists {
		if l.Name == name {
			return l, nil
		}
	}
	return List{}, &missingListError{name, listNames(lists)}
}

var errNoLists = &missingListError{}

type missingListError struct {
	name  string
	known []string
}

func (e *missingListError) Error() string {
	if e.name == "" {
		return "на доске нет списков"
	}
	return "список «" + e.name + "» не найден; есть: " + strings.Join(e.known, ", ")
}

func listNames(lists []List) []string {
	out := make([]string, len(lists))
	for i, l := range lists {
		out[i] = l.Name
	}
	return out
}
