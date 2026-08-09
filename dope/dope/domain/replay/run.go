package replay

import (
	"fmt"
	"sort"
	"strings"
)

// Result is what dope made of a бой for one participant.
type Result struct {
	Place float64
	Total int
}

// Game is the tournament under test. The replayer drives it through this seam
// so the same script can run against a real server, an in-process one, or a
// stub — and so nothing in here knows any SQL.
type Game interface {
	// Seat writes a Draw: these participants take the бой's slots, in order.
	Seat(at Coord, names []string) error
	// Seats reports whom the Structure has seated, in slot order.
	Seats(at Coord) ([]string, error)
	// Play enters one participant's marks into a бой.
	Play(at Coord, name string, marks [][5]Mark) error
	// Pin sets a place the hosts assigned by hand, overriding what the marks
	// score to. Called before Finish, so the бой closes on the host's ruling.
	Pin(at Coord, name string, place float64) error
	// Finish closes a бой so its results reach the rounds that follow.
	Finish(at Coord) error
	// Outcome reports the place and Σ dope computed, by participant.
	Outcome(at Coord) (map[string]Result, error)
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
	return fmt.Sprintf("%s%s: %s — лист %s, у нас %s", f.At, who, f.Field, f.Sheet, f.Ours)
}

// Run replays a transcript against a Game and reports where the two disagree.
//
// It plays бои in written order and closes each one before the next, because a
// round left open seats the round after it from stale results. A seating the
// script marks as a Draw is written; every other seating is checked.
//
// The error return is for a Game that could not be driven at all. A
// disagreement is not an error — it is the output.
func Run(script Script, game Game) ([]Finding, error) {
	silenced := make(map[string]*Override, len(script.Overrides))
	used := map[string]bool{}
	for i := range script.Overrides {
		over := &script.Overrides[i]
		silenced[overrideKey(over.At, over.Field, over.Participant)] = over
	}
	var findings []Finding
	// An override silences the exact disagreement it was written about. One
	// naming a participant covers only that participant; one without a name
	// covers the бой, which is what a seating ruling needs. Either way the key
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

	for _, bout := range script.Bouts {
		names := make([]string, len(bout.Seats))
		for i, seat := range bout.Seats {
			names[i] = seat.Name
		}
		if bout.Draw {
			if err := game.Seat(bout.At, names); err != nil {
				return findings, fmt.Errorf("%s: посадка жребием: %w", bout.At, err)
			}
		} else {
			seated, err := game.Seats(bout.At)
			if err != nil {
				return findings, fmt.Errorf("%s: кто посажен: %w", bout.At, err)
			}
			if !sameSeating(seated, names) {
				report(Finding{
					At:    bout.At,
					Field: "посадка",
					Sheet: strings.Join(names, ", "),
					Ours:  strings.Join(seated, ", "),
					Line:  bout.Line,
				})
			}
		}
		for _, seat := range bout.Seats {
			if err := game.Play(bout.At, seat.Name, seat.Marks); err != nil {
				return findings, fmt.Errorf("%s, %s: отметки: %w", bout.At, seat.Name, err)
			}
		}
		for _, seat := range bout.Seats {
			if !seat.Pinned {
				continue
			}
			if err := game.Pin(bout.At, seat.Name, seat.Place); err != nil {
				return findings, fmt.Errorf("%s, %s: место вручную: %w", bout.At, seat.Name, err)
			}
		}
		if err := game.Finish(bout.At); err != nil {
			return findings, fmt.Errorf("%s: закрытие боя: %w", bout.At, err)
		}
		outcome, err := game.Outcome(bout.At)
		if err != nil {
			return findings, fmt.Errorf("%s: итог боя: %w", bout.At, err)
		}
		// Whom dope scored that the sheet never seated. Checking only the sheet's
		// own names would accept a бой with an extra participant in it.
		for name := range outcome {
			if !hasSeat(bout.Seats, name) {
				report(Finding{At: bout.At, Field: "лишний участник", Participant: name,
					Sheet: "не сидел", Ours: fmt.Sprintf("Σ%d, место %s", outcome[name].Total, place(outcome[name].Place)),
					Line: bout.Line})
			}
		}
		for _, seat := range bout.Seats {
			got, ok := outcome[seat.Name]
			if !ok {
				report(Finding{At: bout.At, Field: "итог", Participant: seat.Name,
					Sheet: fmt.Sprintf("Σ%d, место %s", seat.Total, place(seat.Place)), Ours: "ничего", Line: seat.Line})
				continue
			}
			if got.Total != seat.Total {
				report(Finding{At: bout.At, Field: "Σ", Participant: seat.Name,
					Sheet: fmt.Sprint(seat.Total), Ours: fmt.Sprint(got.Total), Line: seat.Line})
			}
			// A pinned place was written, not derived, so asserting it would only
			// check that dope stored what it was told.
			if got.Place != seat.Place && !seat.Pinned {
				report(Finding{At: bout.At, Field: "место", Participant: seat.Name,
					Sheet: place(seat.Place), Ours: place(got.Place), Line: seat.Line})
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
			At: over.At, Field: "лишнее расхождение", Participant: over.Participant,
			Sheet: over.Reason, Ours: "здесь всё сошлось", Line: over.Line,
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
// its seats by participant, and столы are dealt by lot within a бой.
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
