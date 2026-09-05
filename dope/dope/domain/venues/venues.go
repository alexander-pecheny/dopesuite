// Package venues is the Venue domain (CONTEXT.md): a Venue is a Fest of
// kind 'venue' whose Games are dated Slots, each with its own registration
// link, its applications and their rosters. This file is the pure part — flags,
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

	"dope/dope/platform/util"
	dopestrings "dope/i18nstrings"
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

// The marks a roster's players carry, in the letters a roster table shows.
var (
	FlagCaptain = dopestrings.Default.Venues.Flags.Captain()
	FlagBase    = dopestrings.Default.Venues.Flags.Base()
	FlagLegion  = dopestrings.Default.Venues.Flags.Legion()
)

type RosterPlayer struct {
	PlayerID   int64  `json:"player_id"`
	Surname    string `json:"surname"`
	Name       string `json:"name"`
	Patronymic string `json:"patronymic"`
	// Flag is the mark this player carries in the roster: captain, base-roster
	// player or legionnaire, in the letters the Catalog spells them. It starts
	// as whatever the base roster says and is the filer's to change, because
	// the mirror is a day behind and a player who joined this week is in the
	// team's base roster before it is in buff's copy of it.
	Flag string `json:"flag,omitempty"`
	// Captain is what a roster stored before the flag was a field. Read only:
	// ParseRoster folds it into Flag and nothing writes it again.
	Captain bool `json:"captain,omitempty"`
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
		if p.Flag == "" && p.Captain {
			p.Flag = FlagCaptain
		}
		p.Captain = false
		switch p.Flag {
		case FlagCaptain, FlagBase, FlagLegion:
		default:
			p.Flag = ""
		}
		// A team has one captain or none: the second one marked is not one.
		if p.Flag == FlagCaptain && captain {
			p.Flag = ""
		}
		captain = captain || p.Flag == FlagCaptain
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

// Flags are the marks of a roster, aligned with players: what each player was
// given, and for a roster stored before the flag was a field, what the base
// roster says. base is the team's base roster for the Slot's season, nil when
// the mirror knows none — which reads as legionnaire for a real team and as
// base-roster player for a team without a rating id, since it has no base
// roster to be outside of.
func Flags(players []RosterPlayer, ratingTeamID int64, base map[int64]bool) []string {
	out := make([]string, len(players))
	for i, p := range players {
		switch {
		case p.Flag != "":
			out[i] = p.Flag
		case ratingTeamID == 0, base[p.PlayerID]:
			out[i] = FlagBase
		default:
			out[i] = FlagLegion
		}
	}
	return out
}

// DefaultFlag is the mark a player gets before anyone says otherwise: in the
// team's base roster or not.
func DefaultFlag(playerID, ratingTeamID int64, base map[int64]bool) string {
	if ratingTeamID == 0 || base[playerID] {
		return FlagBase
	}
	return FlagLegion
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

// Registration is the window a Slot takes applications in. One never set up
// takes none; after that the two ends say when, and either may be left off.
func Registration(opensAt, closesAt string, shut bool, now time.Time) RegState {
	if shut {
		return RegClosed
	}
	if closes, ok := ParseTime(closesAt); ok && !now.Before(closes) {
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

// HumanDate is a Slot's own time as a person says it, weekday and all. A Slot
// with no time yet has nothing to say.
func HumanDate(startsAt string) string {
	return util.HumanizeDateTime(FormatTime(startsAt))
}

func HumanTime(stored string) string {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(stored)); err == nil {
		return t.UTC().Format(TimeLayout)
	}
	return FormatTime(stored)
}
