package games

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// A Friendship Cup keeps its players keyed by card: {"<card>": {name, team}}.
// Registering a player is then a patch of his own key, so two hosts at the
// registration desk never overwrite each other's list, and taking one off
// sets his key to null. A document written before that holds a list,
// [{card, name, team}], and still reads.

// KDSeat is what the document holds under a card's key.
type KDSeat struct {
	Name string `json:"name"`
	Team string `json:"team,omitempty"`
}

// KDMaxCard is the highest card n tables tell apart. Card c and card c+n²
// follow the same route, so a card past n² would repeat one already dealt.
func KDMaxCard(n int) int { return n * n }

// KDJoker reports whether card c never leaves its table. Its route moves
// ⌊(c−1)/n⌋ tables a tour, which is no move when that is a multiple of n.
func KDJoker(card, n int) bool {
	return card >= 1 && n >= 1 && ((card-1)/n)%n == 0
}

type kdProblem int

const (
	kdOK kdProblem = iota
	kdBadCard
	kdCardTooHigh
	kdNoName
	kdDuplicate
	kdMalformed
)

// kdEntry is one entry of the players as the document holds it, and what is
// wrong with it, if anything.
type kdEntry struct {
	key     string // the card as written: a map key, or the list item's card
	player  KDPlayer
	problem kdProblem
	removed bool // null under a card key: the player was taken off
}

var errKDPlayersShape = errors.New("friendship cup players are neither a map nor a list")

// kdEntries reads the players in either shape and judges each entry against
// n tables (n ≤ 0 sets no upper bound). Missing or null players are none.
func kdEntries(raw json.RawMessage, n int) ([]kdEntry, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	var out []kdEntry
	switch trimmed[0] {
	case '{':
		var byCard map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &byCard); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(byCard))
		for key := range byCard {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			e := kdEntry{key: key}
			card, err := strconv.Atoi(key)
			if err != nil || strconv.Itoa(card) != key {
				e.problem = kdBadCard
			}
			e.player.Card = card
			value := bytes.TrimSpace(byCard[key])
			if string(value) == "null" {
				e.removed = true
			} else {
				var seat KDSeat
				if err := json.Unmarshal(value, &seat); err != nil && e.problem == kdOK {
					e.problem = kdMalformed
				}
				e.player.Name, e.player.Team = seat.Name, seat.Team
			}
			out = append(out, e)
		}
	case '[':
		var list []json.RawMessage
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, err
		}
		for _, item := range list {
			var probe struct {
				Card json.RawMessage `json:"card"`
				Name string          `json:"name"`
				Team string          `json:"team"`
			}
			e := kdEntry{}
			if err := json.Unmarshal(item, &probe); err != nil {
				e.problem = kdMalformed
			} else {
				e.key = string(bytes.TrimSpace(probe.Card))
				if err := json.Unmarshal(probe.Card, &e.player.Card); err != nil {
					e.problem = kdBadCard
				}
			}
			e.player.Name, e.player.Team = probe.Name, probe.Team
			out = append(out, e)
		}
	default:
		return nil, errKDPlayersShape
	}
	seen := map[int]bool{}
	for i := range out {
		e := &out[i]
		if e.problem != kdOK || e.removed {
			continue
		}
		e.player.Name = strings.TrimSpace(e.player.Name)
		e.player.Team = strings.TrimSpace(e.player.Team)
		switch {
		case e.player.Card < 1:
			e.problem = kdBadCard
		case n > 0 && e.player.Card > KDMaxCard(n):
			e.problem = kdCardTooHigh
		case e.player.Name == "":
			e.problem = kdNoName
		case seen[e.player.Card]:
			e.problem = kdDuplicate
		default:
			seen[e.player.Card] = true
		}
	}
	return out, nil
}

// KDPlayers reads the players a document holds, in card order. Like the page,
// it leaves out every entry that is not a player: a card that is not a whole
// number from 1, a card past what n tables tell apart, a second holder of a
// card, an empty name. n ≤ 0 sets no upper bound on the card.
func KDPlayers(raw json.RawMessage, n int) []KDPlayer {
	entries, err := kdEntries(raw, n)
	if err != nil {
		return nil
	}
	out := make([]KDPlayer, 0, len(entries))
	for _, e := range entries {
		if e.problem == kdOK && !e.removed {
			out = append(out, e.player)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Card < out[j].Card })
	return out
}

// ValidateKDPlayersEdit refuses an edit that leaves a Friendship Cup's players
// wrong, in words the host can act on. It judges only an edit that changed
// the players, so a document stored with a bad entry still takes answers.
// The tables are the document's teams. A card already held may not pass to
// somebody else: that is how a second host, registering at the same time,
// learns the card went to the first.
func ValidateKDPlayersEdit(prevState, nextState []byte) error {
	var prev, next KDState
	if err := json.Unmarshal(prevState, &prev); err != nil {
		prev = KDState{}
	}
	if err := json.Unmarshal(nextState, &next); err != nil {
		return err
	}
	if kdSamePlayers(prev.Players, next.Players) {
		return nil
	}
	n := len(next.Teams)
	entries, err := kdEntries(next.Players, n)
	if err != nil {
		return corei18n.User(dopestrings.Default.Games.Kd.Malformed())
	}
	for _, e := range entries {
		card := strconv.Itoa(e.player.Card)
		switch e.problem {
		case kdBadCard:
			return corei18n.User(dopestrings.Default.Games.Kd.CardInvalid(e.key))
		case kdCardTooHigh:
			return corei18n.User(dopestrings.Default.Games.Kd.CardTooHigh(strconv.Itoa(n), strconv.Itoa(KDMaxCard(n)), card))
		case kdNoName:
			return corei18n.User(dopestrings.Default.Games.Kd.NameMissing(card))
		case kdDuplicate:
			return corei18n.User(dopestrings.Default.Games.Kd.CardDuplicate(card))
		case kdMalformed:
			return corei18n.User(dopestrings.Default.Games.Kd.Malformed())
		}
	}
	held := map[int]string{}
	for _, player := range KDPlayers(prev.Players, 0) {
		held[player.Card] = player.Name
	}
	for _, player := range KDPlayers(next.Players, n) {
		if name, ok := held[player.Card]; ok && name != player.Name {
			return corei18n.User(dopestrings.Default.Games.Kd.CardTaken(strconv.Itoa(player.Card), name))
		}
	}
	return nil
}

func kdSamePlayers(a, b json.RawMessage) bool {
	canon := func(raw json.RawMessage) string {
		if len(bytes.TrimSpace(raw)) == 0 {
			return "null"
		}
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return string(raw)
		}
		out, _ := json.Marshal(v)
		return string(out)
	}
	return canon(a) == canon(b)
}
