// Package venues is the Площадка domain (CONTEXT.md): a Venue is a Fest of
// kind 'venue' whose Games are dated Слоты, each with its own registration
// link, its Заявки and their Составы. This file is the pure part — flags,
// registration state, tokens, the roster document — and the sibling files
// keep the persistence the host and public pages share.
package venues

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

const (
	KindFest  = "fest"
	KindVenue = "venue"
)

const (
	StatusPending  = "pending"
	StatusAccepted = "accepted"
	StatusDeclined = "declined"
)

const (
	FlagCaptain = "К"
	FlagBase    = "Б"
	FlagLegion  = "Л"
)

type RosterPlayer struct {
	PlayerID   int64  `json:"player_id"`
	Surname    string `json:"surname"`
	Name       string `json:"name"`
	Patronymic string `json:"patronymic"`
	Captain    bool   `json:"captain"`
}

func (p RosterPlayer) FullName() string {
	return strings.Join(strings.Fields(p.Surname+" "+p.Name+" "+p.Patronymic), " ")
}

const MaxRoster = 6

func ParseRoster(raw string) []RosterPlayer {
	var players []RosterPlayer
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &players)
	}
	out := make([]RosterPlayer, 0, len(players))
	captain := false
	for _, p := range players {
		p.Surname = strings.TrimSpace(p.Surname)
		p.Name = strings.TrimSpace(p.Name)
		p.Patronymic = strings.TrimSpace(p.Patronymic)
		if p.FullName() == "" {
			continue
		}
		if p.Captain && captain {
			p.Captain = false
		}
		captain = captain || p.Captain
		out = append(out, p)
	}
	return out
}

func MarshalRoster(players []RosterPlayer) string {
	if players == nil {
		players = []RosterPlayer{}
	}
	data, err := json.Marshal(players)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// Flags are the Б/Л/К marks of a Состав, aligned with players. base is the
// team's base roster for the Слот's season, nil when the mirror knows none —
// which reads as легионер for a real team and as основной for a team without
// a rating id, since it has no base roster to be outside of.
func Flags(players []RosterPlayer, ratingTeamID int64, base map[int64]bool) []string {
	out := make([]string, len(players))
	for i, p := range players {
		switch {
		case p.Captain:
			out[i] = FlagCaptain
		case ratingTeamID == 0:
			out[i] = FlagBase
		case base[p.PlayerID]:
			out[i] = FlagBase
		default:
			out[i] = FlagLegion
		}
	}
	return out
}

func FlagSummary(flags []string) string {
	counts := map[string]int{}
	for _, f := range flags {
		counts[f]++
	}
	var parts []string
	for _, f := range []string{FlagCaptain, FlagBase, FlagLegion} {
		if counts[f] > 0 {
			parts = append(parts, strconv.Itoa(counts[f])+f)
		}
	}
	return strings.Join(parts, " ")
}

type RegState int

const (
	RegOpen RegState = iota
	RegScheduled
	RegClosed
)

func Registration(opensAt string, closed bool, now time.Time) RegState {
	if closed {
		return RegClosed
	}
	if opens, ok := ParseTime(opensAt); ok && now.Before(opens) {
		return RegScheduled
	}
	return RegOpen
}

const TimeLayout = "2006-01-02 15:04"

func ParseTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(strings.Replace(value, "T", " ", 1))
	for _, layout := range []string{TimeLayout, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func FormatTime(value string) string {
	if t, ok := ParseTime(value); ok {
		return t.Format(TimeLayout)
	}
	return strings.TrimSpace(value)
}

func Shift(value string, delta time.Duration) string {
	t, ok := ParseTime(value)
	if !ok {
		return value
	}
	return t.Add(delta).Format(TimeLayout)
}

func NewToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return base64.RawURLEncoding.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func HumanTime(stored string) string {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(stored)); err == nil {
		return t.UTC().Format(TimeLayout)
	}
	return FormatTime(stored)
}
