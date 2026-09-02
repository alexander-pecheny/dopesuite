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

// Kind values of the fests row.
const (
	KindFest  = "fest"
	KindVenue = "venue"
)

// Application statuses.
const (
	StatusPending  = "pending"
	StatusAccepted = "accepted"
	StatusDeclined = "declined"
)

// Flags a player carries in a Состав. They are derived, never chosen.
const (
	FlagCaptain = "К"
	FlagBase    = "Б"
	FlagLegion  = "Л"
)

// RosterPlayer is one player of a Состав as the заявка stores them. PlayerID
// 0 means hand-typed: too new for the mirror to know.
type RosterPlayer struct {
	PlayerID   int64  `json:"player_id"`
	Surname    string `json:"surname"`
	Name       string `json:"name"`
	Patronymic string `json:"patronymic"`
	Captain    bool   `json:"captain"`
}

// FullName is «Фамилия Имя Отчество».
func (p RosterPlayer) FullName() string {
	return strings.Join(strings.Fields(p.Surname+" "+p.Name+" "+p.Patronymic), " ")
}

// MaxRoster is how many players a Состав holds before the form warns.
const MaxRoster = 6

// ParseRoster reads a stored roster_json, dropping the nameless rows a
// half-filled form leaves behind and keeping at most one captain.
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

// MarshalRoster is the form a roster is stored in.
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

// FlagSummary counts a Состав's flags for the заявки list: «3Б 1Л» beside the
// captain's К.
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

// RegState is what a registration link says when it is opened.
type RegState int

const (
	// RegOpen accepts заявки.
	RegOpen RegState = iota
	// RegScheduled has not opened yet.
	RegScheduled
	// RegClosed still shows a user their own заявка.
	RegClosed
)

// Registration is the state of a Слот's registration at now. opensAt is the
// stored text ("" — open at once).
func Registration(opensAt string, closed bool, now time.Time) RegState {
	if closed {
		return RegClosed
	}
	if opens, ok := ParseTime(opensAt); ok && now.Before(opens) {
		return RegScheduled
	}
	return RegOpen
}

// TimeLayout is how a Слот's datetime is typed, stored and shown.
const TimeLayout = "2006-01-02 15:04"

// ParseTime reads a stored datetime, accepting the date alone.
func ParseTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(strings.Replace(value, "T", " ", 1))
	for _, layout := range []string{TimeLayout, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// FormatTime normalises a typed datetime for storage; an unparseable one is
// kept as typed so a host never loses what they wrote.
func FormatTime(value string) string {
	if t, ok := ParseTime(value); ok {
		return t.Format(TimeLayout)
	}
	return strings.TrimSpace(value)
}

// Shift moves a stored datetime by delta, keeping an unparseable one as is.
func Shift(value string, delta time.Duration) string {
	t, ok := ParseTime(value)
	if !ok {
		return value
	}
	return t.Add(delta).Format(TimeLayout)
}

// NewToken mints a registration or voting link's unguessable part.
func NewToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return base64.RawURLEncoding.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// HumanTime is a stored timestamp as a page shows it: minutes, no zone.
func HumanTime(stored string) string {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(stored)); err == nil {
		return t.UTC().Format(TimeLayout)
	}
	return FormatTime(stored)
}
