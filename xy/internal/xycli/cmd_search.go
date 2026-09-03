package xycli

import (
	"regexp"
	"strings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
)

// Search reads what the browser's Search Index reads — each Card's 4s, its Alias
// and the comments on it — and matches through xy's Folding, so a stress mark or
// guillemet quotes the typography pass wrote cannot hide a question from the editor
// who typed it plainly. Matching is per line: a hit is a line an editor can act on.

type searchHit struct {
	CardID int64  `json:"card_id"`
	ListID int64  `json:"list_id"`
	Where  string `json:"where"` // card | comment
	Line   string `json:"line"`
}

func cmdSearch(a *app, args []string) error {
	s := xystrings.Default
	fs := a.flags("search", s.Cli.Search.Usage())
	board := a.boardFlag(fs)
	useRegex := fs.Bool("regex", false, s.Cli.Search.RegexFlag())
	cardsOnly := fs.Bool("cards", false, s.Cli.Search.CardsFlag())
	commentsOnly := fs.Bool("comments", false, s.Cli.Search.CommentsFlag())
	rest, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return corei18n.User(s.Cli.Search.NeedQuery())
	}
	query := rest[0]

	var re *regexp.Regexp
	if *useRegex {
		var err error
		if re, err = regexp.Compile(Fold(query)); err != nil {
			return corei18n.User(s.Cli.Search.BadRegex(err.Error()))
		}
	}
	matches := func(line string) bool {
		folded := Fold(line)
		if re != nil {
			return re.MatchString(folded)
		}
		return strings.Contains(folded, Fold(query))
	}

	c, b, err := a.open(*board)
	if err != nil {
		return err
	}
	hits := []searchHit{}
	if !*commentsOnly {
		for _, card := range b.Cards {
			for _, line := range append([]string{card.Alias}, strings.Split(card.Desc, "\n")...) {
				if strings.TrimSpace(line) != "" && matches(line) {
					hits = append(hits, searchHit{CardID: card.ID, ListID: card.ListID, Where: "card", Line: strings.TrimSpace(line)})
				}
			}
		}
	}
	if !*cardsOnly {
		comments, err := c.BoardComments(b.ID)
		if err != nil {
			return err
		}
		byCard := map[int64]int64{}
		for _, card := range b.Cards {
			byCard[card.ID] = card.ListID
		}
		for _, cm := range comments {
			plain, err := b.DK.DecField(cm.PayloadEnc)
			if err != nil {
				continue
			}
			text, _ := decodeCommentPayload(plain)
			for _, line := range strings.Split(text, "\n") {
				if strings.TrimSpace(line) != "" && matches(line) {
					hits = append(hits, searchHit{CardID: cm.CardID, ListID: byCard[cm.CardID], Where: "comment", Line: strings.TrimSpace(line)})
				}
			}
		}
	}
	return a.emit(hits, func() {
		for _, h := range hits {
			mark := " "
			if h.Where == "comment" {
				mark = "💬"
			}
			a.printf("%6d %s %s\n", h.CardID, mark, oneLine(h.Line))
		}
		if len(hits) == 0 {
			a.note("%s", s.Cli.Search.Nothing())
		}
	})
}
