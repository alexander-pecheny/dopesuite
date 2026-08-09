// Package replay reads a tournament transcript — what the hosts actually
// entered, бой by бой — and holds dope to it.
//
// The transcript is a language rather than a dump because the transcript *is*
// the oracle: a fixture nobody can read is not evidence of anything. Every
// tournament arrives in its own flavour of Google Sheets, so each flavour gets
// a small reader that emits this one format, and the replayer never learns what
// an .xlsx is.
//
//	[game]
//	type: ek
//	scheme: ek.dsl
//
//	[roster]
//	1 | Ктулху | Москва
//
//	[s1/r1/w1/m1] жребий
//	Ктулху     | ----- ---R- RR--W | 120 | 1
//
//	override [s1/r2/w1/m1] место: лист сложил три темы из двенадцати
//
// A бой names where it sits — Block, круг, заход, бой — and a seat line carries
// the marks on the left and, past the bars, what the sheet claimed the бой
// produced. The claims are asserted, never applied: dope scores the marks
// itself and must agree.
//
// `жребий` on the бой header says this seating is a Draw — input, written into
// the Edges before play. Without it the seating is derived, and the replay
// checks that the resolver seated exactly these participants.
package replay

import (
	"fmt"
	"strconv"
	"strings"
)

// Mark is one answer cell: taken, lost, or never played.
type Mark byte

const (
	None  Mark = 0
	Right Mark = 'R'
	Wrong Mark = 'W'
)

// Coord is where a бой sits in its Game: which Block, which круг of it, which
// заход of that круг, and which бой of the заход. It is the join key between a
// transcript and dope, and deliberately not "whoever sat at the table" —
// matching by participants only works when the seating is already right, so it
// can never catch a seeding bug.
type Coord struct {
	Block string
	Round int
	Wave  int
	Match int
}

func (c Coord) String() string {
	return fmt.Sprintf("%s/r%d/w%d/m%d", c.Block, c.Round, c.Wave, c.Match)
}

type Entrant struct {
	Number int
	Name   string
	City   string
}

// Seat is one participant's бой: the marks that were entered, then what the
// sheet says those marks came to.
type Seat struct {
	Name  string
	Marks [][5]Mark
	Total int
	Place float64
	Line  int
}

type Bout struct {
	At    Coord
	Draw  bool
	Seats []Seat
	Line  int
}

// Override records a disagreement the tournament's author has ruled on: the
// sheet says one thing, dope another, and this is which to believe and why.
// Its Reason is mandatory — an override without one is a silenced defect.
type Override struct {
	At     Coord
	Field  string
	Reason string
	Line   int
}

type Script struct {
	Game      string
	Title     string
	Scheme    string
	Roster    []Entrant
	Bouts     []Bout
	Overrides []Override
}

func errAt(line int, format string, args ...any) error {
	return fmt.Errorf("строка %d: %s", line, fmt.Sprintf(format, args...))
}

// Parse reads a transcript. It is strict on purpose: a cell it cannot read is
// an error naming the line, never a silent zero, because a silent zero in an
// oracle is worse than no oracle.
func Parse(src string) (Script, error) {
	var script Script
	section := ""
	var bout *Bout

	flush := func() {
		if bout != nil {
			script.Bouts = append(script.Bouts, *bout)
			bout = nil
		}
	}

	for i, raw := range strings.Split(src, "\n") {
		line := i + 1
		text := strings.TrimSpace(stripComment(raw))
		if text == "" {
			continue
		}
		switch {
		case strings.HasPrefix(text, "override "):
			over, err := parseOverride(text, line)
			if err != nil {
				return Script{}, err
			}
			script.Overrides = append(script.Overrides, over)
		case strings.HasPrefix(text, "["):
			head, rest, err := splitHeader(text, line)
			if err != nil {
				return Script{}, err
			}
			switch head {
			case "game", "roster":
				flush()
				section = head
			default:
				at, err := parseCoord(head, line)
				if err != nil {
					return Script{}, err
				}
				flush()
				section = "бой"
				bout = &Bout{At: at, Draw: rest == "жребий", Line: line}
				if rest != "" && rest != "жребий" {
					return Script{}, errAt(line, "после координаты можно писать только «жребий», а не %q", rest)
				}
			}
		case section == "game":
			key, value, ok := strings.Cut(text, ":")
			if !ok {
				return Script{}, errAt(line, "в [game] нужны пары «ключ: значение», а не %q", text)
			}
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			switch key {
			case "type":
				script.Game = value
			case "title":
				script.Title = value
			case "scheme":
				script.Scheme = value
			default:
				return Script{}, errAt(line, "[game] не знает ключа %q", key)
			}
		case section == "roster":
			entrant, err := parseEntrant(text, line)
			if err != nil {
				return Script{}, err
			}
			script.Roster = append(script.Roster, entrant)
		case section == "бой":
			seat, err := parseSeat(text, line)
			if err != nil {
				return Script{}, err
			}
			bout.Seats = append(bout.Seats, seat)
		default:
			return Script{}, errAt(line, "строка вне секции: %q", text)
		}
	}
	flush()
	return script, nil
}

func stripComment(raw string) string {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		return raw[:i]
	}
	return raw
}

// splitHeader takes `[s1/r1/w1/m1] жребий` apart into its bracketed head and
// whatever follows.
func splitHeader(text string, line int) (string, string, error) {
	end := strings.IndexByte(text, ']')
	if end < 0 {
		return "", "", errAt(line, "не закрыта скобка в %q", text)
	}
	return strings.TrimSpace(text[1:end]), strings.TrimSpace(text[end+1:]), nil
}

// parseCoord reads `s1/r1/w1/m1`. All four coordinates are required: a бой that
// does not say which заход it is cannot be told from its twin.
func parseCoord(text string, line int) (Coord, error) {
	parts := strings.Split(text, "/")
	if len(parts) != 4 {
		return Coord{}, errAt(line, "координата — это блок/круг/заход/бой, например s1/r1/w1/m1, а не %q", text)
	}
	coord := Coord{Block: parts[0]}
	if coord.Block == "" {
		return Coord{}, errAt(line, "координата без блока: %q", text)
	}
	for i, part := range []struct {
		prefix string
		into   *int
		what   string
	}{
		{"r", &coord.Round, "круг"},
		{"w", &coord.Wave, "заход"},
		{"m", &coord.Match, "бой"},
	} {
		field := parts[i+1]
		if !strings.HasPrefix(field, part.prefix) {
			return Coord{}, errAt(line, "%s пишется как %s1, а не %q", part.what, part.prefix, field)
		}
		n, err := strconv.Atoi(field[len(part.prefix):])
		if err != nil || n < 1 {
			return Coord{}, errAt(line, "%s должен быть номером от 1, а не %q", part.what, field)
		}
		*part.into = n
	}
	return coord, nil
}

// parseEntrant reads `1 | Ктулху | Москва` — number, name, optional city. The
// bars are what a seat line uses too, and they are here for the same reason:
// «Ушки на макушке Казань» has no unambiguous reading without them.
func parseEntrant(text string, line int) (Entrant, error) {
	fields := strings.Split(text, "|")
	if len(fields) < 2 || len(fields) > 3 {
		return Entrant{}, errAt(line, "участник — «номер | название | город», а не %q", text)
	}
	number, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil || number < 1 {
		return Entrant{}, errAt(line, "номер участника — целое от 1, а не %q", strings.TrimSpace(fields[0]))
	}
	entrant := Entrant{Number: number, Name: strings.TrimSpace(fields[1])}
	if entrant.Name == "" {
		return Entrant{}, errAt(line, "участник без названия")
	}
	if len(fields) == 3 {
		entrant.City = strings.TrimSpace(fields[2])
	}
	return entrant, nil
}

// parseSeat reads `Ктулху | ----- ---R- RR--W | 120 | 1`: who sat there, what
// they took, and the sheet's Σ and место for them.
func parseSeat(text string, line int) (Seat, error) {
	fields := strings.Split(text, "|")
	if len(fields) != 4 {
		return Seat{}, errAt(line, "место в бою — «кто | метки | Σ | место», а не %q", text)
	}
	seat := Seat{Name: strings.TrimSpace(fields[0]), Line: line}
	if seat.Name == "" {
		return Seat{}, errAt(line, "место без участника")
	}
	for _, theme := range strings.Fields(fields[1]) {
		marks, err := parseTheme(theme, line)
		if err != nil {
			return Seat{}, err
		}
		seat.Marks = append(seat.Marks, marks)
	}
	if len(seat.Marks) == 0 {
		return Seat{}, errAt(line, "у %s нет ни одной темы", seat.Name)
	}
	total, err := strconv.Atoi(strings.TrimSpace(fields[2]))
	if err != nil {
		return Seat{}, errAt(line, "Σ у %s — целое число, а не %q", seat.Name, strings.TrimSpace(fields[2]))
	}
	seat.Total = total
	place, err := strconv.ParseFloat(strings.TrimSpace(fields[3]), 64)
	if err != nil || place <= 0 {
		return Seat{}, errAt(line, "место у %s — число от 1 (делённое место пишется как 1.5), а не %q",
			seat.Name, strings.TrimSpace(fields[3]))
	}
	seat.Place = place
	return seat, nil
}

// parseTheme reads one theme's five cells: R taken, W lost, - never played.
func parseTheme(text string, line int) ([5]Mark, error) {
	var marks [5]Mark
	if len(text) != 5 {
		return marks, errAt(line, "в теме пять ответов, а в %q — %d", text, len(text))
	}
	for i := 0; i < 5; i++ {
		switch text[i] {
		case '-':
			marks[i] = None
		case 'R', 'r':
			marks[i] = Right
		case 'W', 'w':
			marks[i] = Wrong
		default:
			return marks, errAt(line, "в теме %q знак %q — бывают только R, W и -", text, text[i:i+1])
		}
	}
	return marks, nil
}

// parseOverride reads `override [s1/r2/w1/m1] место: причина`.
func parseOverride(text string, line int) (Override, error) {
	head, rest, err := splitHeader(strings.TrimSpace(strings.TrimPrefix(text, "override")), line)
	if err != nil {
		return Override{}, err
	}
	at, err := parseCoord(head, line)
	if err != nil {
		return Override{}, err
	}
	field, reason, ok := strings.Cut(rest, ":")
	field, reason = strings.TrimSpace(field), strings.TrimSpace(reason)
	if !ok || field == "" || reason == "" {
		return Override{}, errAt(line,
			"расхождение пишется как «override [координата] поле: почему листу верить нельзя» — без причины это молча спрятанная ошибка")
	}
	return Override{At: at, Field: field, Reason: reason, Line: line}, nil
}
