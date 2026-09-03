package replay

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	dopestrings "dope/i18nstrings"
	corei18n "pecheny.me/dopecore/i18nstrings"
)

// Result is what dope made of a Match for one participant.
type Result struct {
	Place float64
	Total int
}

// Game is the tournament under test. The replayer drives it through this seam
// so the same script can run against a real server, an in-process one, or a
// stub — and so nothing in here knows any SQL.
type Game interface {
	// Seat writes a Draw: these participants take the Match's slots, in order.
	Seat(at Coord, names []string) error
	// Seats reports whom the Structure has seated, in slot order.
	Seats(at Coord) ([]string, error)
	// Play enters one participant's side of a Match.
	Play(at Coord, name string, play Play) error
	// Pin sets a place the hosts assigned by hand, overriding what the marks
	// score to. Called before Finish, so the Match closes on the host's ruling.
	Pin(at Coord, name string, place float64) error
	// Finish closes a Match so its results reach the rounds that follow.
	Finish(at Coord) error
	// Outcome reports the place and Σ dope computed, by participant.
	Outcome(at Coord) (map[string]Result, error)
}

// LineupWriter is the half of Game a transcript with [lineups] (sostavy) needs: register
// each team's players before the first Match, so the theme players have somebody
// to be. Optional — a Game that cannot take lineups fails such a script.
type LineupWriter interface {
	Lineups(lineups []Lineup) error
}

// StatsReader is the half of Game a transcript with [statistics] (statistika) needs: the
// per-player aggregates dope computed over the whole game, in the same three
// columns the transcript's section carries. Optional, like LineupWriter.
type StatsReader interface {
	PlayerStats() ([]Stat, error)
}

// StandingsReader is the half of Game a transcript with [standings] (tablitsa) needs: the
// table dope ranked for a Block, or a Group in it, as rows of the place it
// shows (shared when level) and who holds it. Optional, like StatsReader.
type StandingsReader interface {
	Standings(at Coord) ([]TableRow, error)
}

// Play is what one participant did in a Match, in whichever of the two shapes the
// game uses. It carries the play data alone — the sheet's Σ and place stay out,
// so a Game cannot quietly apply the answer it is supposed to be checked against.
// Players, when the transcript carries them, name who played each theme,
// aligned with Themes.
type Play struct {
	Themes    [][5]Mark
	Players   []string
	Questions []Answer
	Counts    [][]int
	// Shootout is the net shootout points, zero for a seat that played none.
	Shootout int
}

// Finding is one disagreement between the sheet and dope. It always shows both
// sides: which is wrong is a judgement, and the replayer does not make it.
type Finding struct {
	At          Coord
	Field       string
	Participant string
	Sheet       string
	Ours        string
	Line        int
}

func (f Finding) String() string {
	who := ""
	if f.Participant != "" {
		who = ", " + f.Participant
	}
	return dopestrings.Default.Replay.Run.Finding(fmt.Sprint(f.At), who, f.Field, f.Sheet, f.Ours)
}

// Run replays a transcript against a Game and reports where the two disagree.
//
// It plays Matches in written order and closes each one before the next, because a
// round left open seats the round after it from stale results. A seating the
// script marks as a Draw is written; every other seating is checked.
//
// The error return is for a Game that could not be driven at all. A
// disagreement is not an error — it is the output.
func Run(script Script, game Game) ([]Finding, error) {
	s := dopestrings.Default
	silenced := make(map[string]*Override, len(script.Overrides))
	used := map[string]bool{}
	for i := range script.Overrides {
		over := &script.Overrides[i]
		silenced[overrideKey(over.At, over.Field, over.Participant)] = over
	}
	var findings []Finding
	// An override silences the exact disagreement it was written about. One
	// naming a participant covers only that participant; one without a name
	// covers the whole Match, which is what a seating ruling needs. Either way the key
	// is recorded as used, so an override that matched nothing can be reported
	// rather than sitting on the discrepancies page as a reviewed deviation.
	report := func(f Finding) {
		for _, key := range []string{
			overrideKey(f.At, f.Field, f.Participant),
			overrideKey(f.At, f.Field, ""),
		} {
			if silenced[key] != nil {
				used[key] = true
				return
			}
		}
		findings = append(findings, f)
	}

	if len(script.Lineups) > 0 {
		writer, ok := game.(LineupWriter)
		if !ok {
			return findings, corei18n.User(s.Replay.Run.LineupsUnwritable())
		}
		if err := writer.Lineups(script.Lineups); err != nil {
			return findings, corei18n.User(s.Replay.Run.LineupsWrap(err.Error()))
		}
	}

	for _, bout := range script.Bouts {
		names := make([]string, len(bout.Seats))
		for i, seat := range bout.Seats {
			names[i] = seat.Name
		}
		if bout.Draw {
			if err := game.Seat(bout.At, names); err != nil {
				return findings, corei18n.User(s.Replay.Run.SeatDrawWrap(fmt.Sprint(bout.At), err.Error()))
			}
		} else {
			seated, err := game.Seats(bout.At)
			if err != nil {
				return findings, corei18n.User(s.Replay.Run.SeatedWrap(fmt.Sprint(bout.At), err.Error()))
			}
			if !sameSeating(seated, names) {
				report(Finding{
					At:    bout.At,
					Field: s.Replay.Run.FieldSeating(),
					Sheet: strings.Join(names, ", "),
					Ours:  strings.Join(seated, ", "),
					Line:  bout.Line,
				})
			}
		}
		for _, seat := range bout.Seats {
			if err := game.Play(bout.At, seat.Name, Play{Themes: seat.Marks, Players: seat.Players, Questions: seat.Questions, Counts: seat.Counts, Shootout: seat.Shootout}); err != nil {
				return findings, corei18n.User(s.Replay.Run.PlayWrap(fmt.Sprint(bout.At), seat.Name, err.Error()))
			}
		}
		for _, seat := range bout.Seats {
			if !seat.Pinned {
				continue
			}
			if err := game.Pin(bout.At, seat.Name, seat.Place); err != nil {
				return findings, corei18n.User(s.Replay.Run.PinWrap(fmt.Sprint(bout.At), seat.Name, err.Error()))
			}
		}
		if err := game.Finish(bout.At); err != nil {
			return findings, corei18n.User(s.Replay.Run.FinishWrap(fmt.Sprint(bout.At), err.Error()))
		}
		outcome, err := game.Outcome(bout.At)
		if err != nil {
			return findings, corei18n.User(s.Replay.Run.OutcomeWrap(fmt.Sprint(bout.At), err.Error()))
		}
		// Whom dope scored that the sheet never seated. Checking only the sheet's
		// own names would accept a Match with an extra participant in it.
		for name := range outcome {
			if !hasSeat(bout.Seats, name) {
				report(Finding{At: bout.At, Field: s.Replay.Run.FieldExtra(), Participant: name,
					Sheet: s.Replay.Run.NotSeated(), Ours: s.Replay.Run.SheetScore(strconv.Itoa(outcome[name].Total), place(outcome[name].Place)),
					Line: bout.Line})
			}
		}
		for _, seat := range bout.Seats {
			got, ok := outcome[seat.Name]
			if !ok {
				report(Finding{At: bout.At, Field: s.Replay.Run.FieldOutcome(), Participant: seat.Name,
					Sheet: s.Replay.Run.SheetScore(strconv.Itoa(seat.Total), place(seat.Place)), Ours: s.Replay.Run.Nothing(), Line: seat.Line})
				continue
			}
			if got.Total != seat.Total {
				report(Finding{At: bout.At, Field: "Σ", Participant: seat.Name,
					Sheet: fmt.Sprint(seat.Total), Ours: fmt.Sprint(got.Total), Line: seat.Line})
			}
			// A pinned place was written, not derived, so asserting it would only
			// check that dope stored what it was told; an unranked one the sheet
			// never printed, so there is nothing to hold dope to.
			if got.Place != seat.Place && !seat.Pinned && !seat.Unranked {
				report(Finding{At: bout.At, Field: s.Replay.Run.FieldPlace(), Participant: seat.Name,
					Sheet: place(seat.Place), Ours: place(got.Place), Line: seat.Line})
			}
		}
	}
	// Stats are asserted the way Σ and place are, once, after the last Match:
	// dope aggregates the game itself and has to agree with the sheet player by
	// player — including players only one side knows about.
	if len(script.Stats) > 0 {
		reader, ok := game.(StatsReader)
		if !ok {
			return findings, corei18n.User(s.Replay.Run.StatsUncomputable())
		}
		ours, err := reader.PlayerStats()
		if err != nil {
			return findings, corei18n.User(s.Replay.Run.StatsWrap(err.Error()))
		}
		codec, _ := CodecFor(script.Game)
		columns := codec.Columns
		statKey := func(s Stat) string { return s.Player + "\x1f" + s.Team }
		oursBy := make(map[string]Stat, len(ours))
		for _, stat := range ours {
			oursBy[statKey(stat)] = stat
		}
		sheetKeys := make(map[string]bool, len(script.Stats))
		for _, want := range script.Stats {
			sheetKeys[statKey(want)] = true
			got, ok := oursBy[statKey(want)]
			if !ok {
				report(Finding{At: StatsCoord, Field: s.Replay.Run.FieldStats(), Participant: want.Player,
					Sheet: fmt.Sprint(want.Values), Ours: s.Replay.Run.Nothing(), Line: want.Line})
				continue
			}
			for i := range want.Values {
				if want.Values[i] != got.Values[i] {
					report(Finding{At: StatsCoord, Field: columns[i], Participant: want.Player,
						Sheet: fmt.Sprint(want.Values[i]), Ours: fmt.Sprint(got.Values[i]), Line: want.Line})
				}
			}
		}
		for _, stat := range ours {
			if !sheetKeys[statKey(stat)] {
				report(Finding{At: StatsCoord, Field: s.Replay.Run.FieldStats(), Participant: stat.Player,
					Sheet: s.Replay.Run.NoRow(), Ours: fmt.Sprint(stat.Values)})
			}
		}
	}

	// A table is asserted like the stats: once, both ways.
	if len(script.Tables) > 0 {
		reader, ok := game.(StandingsReader)
		if !ok {
			return findings, corei18n.User(s.Replay.Run.TableUnrankable())
		}
		for _, table := range script.Tables {
			ours, err := reader.Standings(table.At)
			if err != nil {
				return findings, fmt.Errorf("%s: %w", table.At, err)
			}
			oursBy := make(map[string]float64, len(ours))
			for _, row := range ours {
				oursBy[row.Name] = row.Place
			}
			listed := make(map[string]bool, len(table.Rows))
			for _, want := range table.Rows {
				listed[want.Name] = true
				got, ok := oursBy[want.Name]
				if !ok {
					report(Finding{At: table.At, Field: s.Replay.Run.FieldTable(), Participant: want.Name,
						Sheet: place(want.Place), Ours: s.Replay.Run.NoRow(), Line: want.Line})
				} else if got != want.Place {
					report(Finding{At: table.At, Field: s.Replay.Run.FieldPlace(), Participant: want.Name,
						Sheet: place(want.Place), Ours: place(got), Line: want.Line})
				}
			}
			for _, row := range ours {
				if !listed[row.Name] {
					report(Finding{At: table.At, Field: s.Replay.Run.FieldTable(), Participant: row.Name,
						Sheet: s.Replay.Run.NoRow(), Ours: place(row.Place), Line: table.Line})
				}
			}
		}
	}

	// An override nobody needed is a claim that something is wrong when it is
	// not — on the discrepancies page it reads as a reviewed deviation, so it
	// has to be reported like any other disagreement.
	for key, over := range silenced {
		if used[key] {
			continue
		}
		findings = append(findings, Finding{
			At: over.At, Field: s.Replay.Run.FieldUnneeded(), Participant: over.Participant,
			Sheet: over.Reason, Ours: s.Replay.Run.AllAgreed(), Line: over.Line,
		})
	}
	sort.SliceStable(findings, func(a, b int) bool { return findings[a].Line < findings[b].Line })
	return findings, nil
}

func overrideKey(at Coord, field, participant string) string {
	return at.String() + "|" + field + "|" + participant
}

func hasSeat(seats []Seat, name string) bool {
	for _, seat := range seats {
		if seat.Name == name {
			return true
		}
	}
	return false
}

// sameSeating compares who is at the table, not in what order: a Protocol reads
// its seats by participant, and tables are dealt by lot within a Match.
func sameSeating(ours, sheet []string) bool {
	if len(ours) != len(sheet) {
		return false
	}
	a, b := append([]string(nil), ours...), append([]string(nil), sheet...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func place(value float64) string {
	if value == float64(int(value)) {
		return fmt.Sprint(int(value))
	}
	return fmt.Sprintf("%.1f", value)
}
