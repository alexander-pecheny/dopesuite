// Package replay reads a tournament transcript — what the hosts actually
// entered, Match by Match — and holds dope to it.
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
// A Match names where it sits — Block, Round, Wave, Match — and a seat line carries
// the marks on the left and, past the bars, what the sheet claimed the Match
// produced. The claims are asserted, never applied: dope scores the marks
// itself and must agree.
//
// `жребий` on the Match header says this seating is a Draw — input, written into
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

// Coord is where a Match sits in its Game: which Block, which Group of it (only
// where a Block has them), which Round, which Wave of that Round, and which Match
// of the Wave. It is the join key between a transcript and dope, and
// deliberately not "whoever sat at the table" — matching by participants only
// works when the seating is already right, so it can never catch a seeding bug.
//
// The Group is not optional decoration: individual SI's six Groups all sit at Block
// s1, Wave 1, Rounds 1..4, so without it one coordinate names six different
// Matches and five of them are never checked.
type Coord struct {
	Block string
	Group string
	Round int
	Wave  int
	Match int
}

func (c Coord) String() string {
	if c.Round == 0 {
		// Not a Match at all (a real Round starts at 1): the stats
		// pseudo-coordinate, or a Block's table.
		if c.Block == StatsCoord.Block {
			return c.Block
		}
		return "таблица " + c.stage()
	}
	if c.Group == "" {
		return fmt.Sprintf("%s/r%d/w%d/m%d", c.Block, c.Round, c.Wave, c.Match)
	}
	return fmt.Sprintf("%s/g%s/r%d/w%d/m%d", c.Block, c.Group, c.Round, c.Wave, c.Match)
}

// stage names the Block, or the Group in it, a table coordinate points at.
func (c Coord) stage() string {
	if c.Group == "" {
		return c.Block
	}
	return c.Block + "/g" + c.Group
}

type Entrant struct {
	Number int
	Name   string
	City   string
}

// Lineup is one team's players, in roster order. It is input the replay
// registers, and the fence the theme players and the stats are checked
// against at the door.
type Lineup struct {
	Team    string
	Players []string
	Line    int
}

// Stat is one line of the sheet's own per-player aggregates, asserted after
// the last Match the way Σ and place are asserted after each one. What the three
// numbers mean is the game's affair: EK writes Σ, positive themes and themes
// played; brain — attempts, right, wrong; individual SI — Σ, Σ+ and Matches sat.
type Stat struct {
	Player string
	Team   string
	Values [3]int
	Line   int
}

// StatsCoord is the coordinate the stats findings and overrides live at: the
// aggregates hold the whole game, so no Match coordinate can name them.
var StatsCoord = Coord{Block: "статистика"}

// Table is the sheet's standings of one Block or Group — `[таблица s1/g3]` —
// asserted after the last Match the way the stats are.
type Table struct {
	At   Coord
	Rows []TableRow
	Line int
}

// TableRow is one line of a table: a place (shared when level) and who holds it.
type TableRow struct {
	Place float64
	Name  string
	Line  int
}

// Answer is one buzzer question of a brain Match from one side: whether they took
// it, and who buzzed. The sheets do not always record the player, so a taken
// question with nobody named is ordinary data rather than a hole.
type Answer struct {
	Mark   Mark
	Player string
}

// Seat is one participant's Match: what was entered, then what the sheet says it
// came to.
//
// The entered part has two shapes, and a Match is one or the other. Marks is the
// theme grid of EK and SI — five cells per theme. Questions is brain's
// duel over buzzer questions, where a cell also names who took it.
type Seat struct {
	Name      string
	Marks     [][5]Mark
	Questions []Answer
	// Counts is Troika's grid: per theme, per question, how many of the three
	// answered it. The seats behind a count are not in the sheet, so the
	// driver synthesizes them — total faithful, composition invented, exactly
	// as a shootout's marks are.
	Counts [][]int
	Total  int
	Place  float64
	// Pinned marks a place the hosts set by hand rather than one the marks
	// imply — written `3!`. A shootout breaks a tie with material the
	// protocol grid never records, so the place is input, exactly as a Draw is:
	// the replayer writes it and does not assert it.
	Pinned bool
	// Unranked marks a seat whose place the sheet never printed — TPSh's written
	// qualifier prints Σ and leaves the place to a standings tab. There is nothing to
	// hold dope to, so the replay checks the Σ and says nothing about the place.
	Unranked bool
	// Players names who played each theme, aligned with Marks — EK's fifth
	// field. Empty string is a theme the sheet named nobody for; a nil slice is
	// a transcript that does not carry players at all.
	Players []string
	// Shootout is the net shootout points the sheet recorded for this seat —
	// extra material the theme grid never holds, written as its own line inside
	// the Match. It is input, like a Draw: dope replays it and ranks with it.
	Shootout int
	Line     int
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
	At          Coord
	Field       string
	Participant string
	Reason      string
	Line        int
}

type Script struct {
	Game      string
	Title     string
	Scheme    string
	Roster    []Entrant
	Lineups   []Lineup
	Stats     []Stat
	Tables    []Table
	Bouts     []Bout
	Overrides []Override
}

// individual reports whether the game seats players rather than teams — no
// lineups, no theme players, no team column in the stats.
func (s Script) individual() bool {
	codec, _ := CodecFor(s.Game)
	return codec.Individual
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
	seen := map[string]int{}
	var failure error

	// flush closes the Match being read. A Match with no seats is refused here
	// rather than replayed: it would seat nobody, assert nothing and pass, which
	// is precisely how a truncated transcript reads as a clean tournament.
	flush := func() {
		if bout == nil {
			return
		}
		if len(bout.Seats) == 0 && failure == nil {
			failure = errAt(bout.Line, "бой %s без единого места — оборванная стенограмма прошла бы молча", bout.At)
		}
		script.Bouts = append(script.Bouts, *bout)
		bout = nil
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
			case "game", "roster", "составы", "статистика":
				flush()
				section = head
			default:
				at, err := parseHead(head, line)
				if err != nil {
					return Script{}, err
				}
				flush()
				if failure != nil {
					return Script{}, failure
				}
				if prev, taken := seen[at.String()]; taken {
					return Script{}, errAt(line,
						"%s уже есть на строке %d — на одной координате одна запись, иначе одна из них молча пропадёт", at, prev)
				}
				seen[at.String()] = line
				if at.Round == 0 {
					if rest != "" {
						return Script{}, errAt(line, "после таблицы ничего не пишут, а тут %q", rest)
					}
					section = "таблица"
					script.Tables = append(script.Tables, Table{At: at, Line: line})
					continue
				}
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
		case section == "составы":
			if script.individual() {
				return Script{}, errAt(line, "в личной игре нет составов — участник и есть игрок")
			}
			lineup, err := parseLineup(text, line)
			if err != nil {
				return Script{}, err
			}
			script.Lineups = append(script.Lineups, lineup)
		case section == "статистика":
			stat, err := parseStat(text, line, script.individual())
			if err != nil {
				return Script{}, err
			}
			script.Stats = append(script.Stats, stat)
		case section == "таблица":
			row, err := parseTableRow(text, line)
			if err != nil {
				return Script{}, err
			}
			table := &script.Tables[len(script.Tables)-1]
			table.Rows = append(table.Rows, row)
		case section == "бой" && strings.HasPrefix(text, "перестрелка "):
			if err := parseShootout(text, line, bout); err != nil {
				return Script{}, err
			}
		case section == "бой":
			codec, ok := CodecFor(script.Game)
			if !ok {
				return Script{}, errAt(line, "у игры %q нет формы боя в стенограмме", script.Game)
			}
			seat, err := parseSeat(text, line, codec)
			if err != nil {
				return Script{}, err
			}
			bout.Seats = append(bout.Seats, seat)
		default:
			return Script{}, errAt(line, "строка вне секции: %q", text)
		}
	}
	flush()
	if failure != nil {
		return Script{}, failure
	}
	if err := checkTables(script); err != nil {
		return Script{}, err
	}
	if err := checkRoster(script); err != nil {
		return Script{}, err
	}
	return script, nil
}

// checkTables refuses a table with no rows (it would assert nothing and pass)
// and one that ranks somebody twice.
func checkTables(script Script) error {
	for _, table := range script.Tables {
		if len(table.Rows) == 0 {
			return errAt(table.Line, "%s без единой строки — оборванная стенограмма прошла бы молча", table.At)
		}
		seen := map[string]int{}
		for _, row := range table.Rows {
			if prev, taken := seen[row.Name]; taken {
				return errAt(row.Line, "%s: %q уже стоит на строке %d", table.At, row.Name, prev)
			}
			seen[row.Name] = row.Line
		}
	}
	return nil
}

// stripComment drops a whole-line comment only. A '#' mid-line stays: teams are
// called things like «Решётка #1», and silently truncating one turns every Match
// it played into an unexplained seating disagreement.
func stripComment(raw string) string {
	if strings.HasPrefix(strings.TrimSpace(raw), "#") {
		return ""
	}
	return raw
}

// checkRoster holds seat lines to the roster where one is given. A misspelt
// name would otherwise reach the Game, which would report it as a seating
// disagreement — a real defect's symptom pinned on a typo.
func checkRoster(script Script) error {
	if len(script.Roster) == 0 {
		return nil
	}
	known := make(map[string]bool, len(script.Roster))
	numbers := make(map[int]string, len(script.Roster))
	for _, entrant := range script.Roster {
		if known[entrant.Name] {
			return fmt.Errorf("участник %q записан в [roster] дважды", entrant.Name)
		}
		if prev, taken := numbers[entrant.Number]; taken {
			return fmt.Errorf("номер %d занят и %q, и %q", entrant.Number, prev, entrant.Name)
		}
		known[entrant.Name] = true
		numbers[entrant.Number] = entrant.Name
	}
	for _, bout := range script.Bouts {
		for _, seat := range bout.Seats {
			if !known[seat.Name] {
				return errAt(seat.Line, "в бою %s сидит %q, которого нет в [roster]", bout.At, seat.Name)
			}
		}
	}
	for _, table := range script.Tables {
		for _, row := range table.Rows {
			if !known[row.Name] {
				return errAt(row.Line, "в %s стоит %q, которого нет в [roster]", table.At, row.Name)
			}
		}
	}
	return checkLineups(script, known)
}

// checkLineups holds lineups, theme players and stats to each other: a
// lineup names a rostered team, a theme player and a stats line name
// someone from his team's lineup. A misspelt name held only by the Game would
// surface as a defect somewhere downstream; here it is a typo with a line
// number.
func checkLineups(script Script, teams map[string]bool) error {
	players := map[string]map[string]bool{}
	for _, lineup := range script.Lineups {
		if !teams[lineup.Team] {
			return errAt(lineup.Line, "состав команды %q, которой нет в [roster]", lineup.Team)
		}
		if players[lineup.Team] != nil {
			return errAt(lineup.Line, "состав %s записан дважды", lineup.Team)
		}
		names := make(map[string]bool, len(lineup.Players))
		for _, name := range lineup.Players {
			names[name] = true
		}
		players[lineup.Team] = names
	}
	if len(script.Lineups) > 0 {
		for _, bout := range script.Bouts {
			for _, seat := range bout.Seats {
				for theme, name := range seat.Players {
					if name != "" && !players[seat.Name][name] {
						return errAt(seat.Line, "тему %d у %s играет %q, которого нет в его составе", theme+1, seat.Name, name)
					}
				}
			}
		}
	}
	for _, stat := range script.Stats {
		if stat.Team == "" {
			if !teams[stat.Player] {
				return errAt(stat.Line, "статистика %q, которого нет в [roster]", stat.Player)
			}
			continue
		}
		if !teams[stat.Team] {
			return errAt(stat.Line, "статистика команды %q, которой нет в [roster]", stat.Team)
		}
		if len(script.Lineups) > 0 && !players[stat.Team][stat.Player] {
			return errAt(stat.Line, "статистика %q, которого нет в составе %s", stat.Player, stat.Team)
		}
	}
	return nil
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

// parseHead reads what a section header or an override points at: a Match's
// coordinate, `таблица s1/g3` for a Block's or Group's standings, or
// `статистика`.
func parseHead(head string, line int) (Coord, error) {
	if head == StatsCoord.Block {
		return StatsCoord, nil
	}
	if stage, ok := strings.CutPrefix(head, "таблица "); ok {
		block, group, _ := strings.Cut(strings.TrimSpace(stage), "/")
		if block == "" || (group != "" && (!strings.HasPrefix(group, "g") || len(group) < 2 || strings.Contains(group, "/"))) {
			return Coord{}, errAt(line, "таблица — это блок или группа в нём, например «таблица s1» или «таблица s1/g3», а не %q", stage)
		}
		return Coord{Block: block, Group: strings.TrimPrefix(group, "g")}, nil
	}
	return parseCoord(head, line)
}

func parseTableRow(text string, line int) (TableRow, error) {
	placeText, name, ok := strings.Cut(text, "|")
	name = strings.TrimSpace(name)
	place, err := strconv.ParseFloat(strings.TrimSpace(placeText), 64)
	if !ok || name == "" || err != nil || place <= 0 {
		return TableRow{}, errAt(line, "строка таблицы — «место | участник» (делённое место как 1.5), а не %q", text)
	}
	return TableRow{Place: place, Name: name, Line: line}, nil
}

// parseCoord reads `s1/r1/w1/m1`, or `s1/g3/r1/w1/m1` in a Block that has
// Groups. Round, Wave and Match are always required: a Match that does not say
// which Wave it is cannot be told from its twin.
func parseCoord(text string, line int) (Coord, error) {
	parts := strings.Split(text, "/")
	coord := Coord{Block: parts[0]}
	if len(parts) == 5 {
		if !strings.HasPrefix(parts[1], "g") || len(parts[1]) < 2 {
			return Coord{}, errAt(line, "группа пишется как g3, а не %q", parts[1])
		}
		coord.Group = parts[1][1:]
		parts = append(parts[:1], parts[2:]...)
	}
	if len(parts) != 4 {
		return Coord{}, errAt(line,
			"координата — это блок[/группа]/круг/заход/бой, например s1/r1/w1/m1 или s1/g3/r1/w1/m1, а не %q", text)
	}
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

// parseLineup reads `Ктулху | Иван Петров, Анна Ким` — a team and its players,
// comma-separated because a player's name has a space in the middle of it.
func parseLineup(text string, line int) (Lineup, error) {
	team, rest, ok := strings.Cut(text, "|")
	lineup := Lineup{Team: strings.TrimSpace(team), Line: line}
	if !ok || lineup.Team == "" {
		return Lineup{}, errAt(line, "состав — «команда | игрок, игрок, …», а не %q", text)
	}
	seen := map[string]bool{}
	for _, name := range strings.Split(rest, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			return Lineup{}, errAt(line, "в составе %s пустое имя", lineup.Team)
		}
		if seen[name] {
			return Lineup{}, errAt(line, "в составе %s игрок %q записан дважды", lineup.Team, name)
		}
		seen[name] = true
		lineup.Players = append(lineup.Players, name)
	}
	if len(lineup.Players) == 0 {
		return Lineup{}, errAt(line, "состав %s без единого игрока", lineup.Team)
	}
	return lineup, nil
}

// parseStat reads one line of the sheet's aggregates: `Игрок | Команда | a | b
// | c` in a team game, `Игрок | a | b | c` in an individual one, where the
// participant already is the player.
func parseStat(text string, line int, individual bool) (Stat, error) {
	fields := strings.Split(text, "|")
	want := 5
	if individual {
		want = 4
	}
	if len(fields) != want {
		return Stat{}, errAt(line, "строка статистики — %d полей через |, а не %q", want, text)
	}
	stat := Stat{Player: strings.TrimSpace(fields[0]), Line: line}
	if stat.Player == "" {
		return Stat{}, errAt(line, "строка статистики без игрока")
	}
	numbers := fields[1:]
	if !individual {
		stat.Team = strings.TrimSpace(fields[1])
		if stat.Team == "" {
			return Stat{}, errAt(line, "статистика %s без команды", stat.Player)
		}
		numbers = fields[2:]
	}
	for i, field := range numbers {
		value, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil {
			return Stat{}, errAt(line, "статистика %s — числа, а не %q", stat.Player, strings.TrimSpace(field))
		}
		stat.Values[i] = value
	}
	return stat, nil
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
// they took, and the sheet's Σ and place for them. In a brain the middle field
// is the Match's questions instead — `R Виктория Корнеева, -, W Санжи Сундуев`.
// An EK line may carry a fifth field naming who played each theme.
func parseSeat(text string, line int, codec Codec) (Seat, error) {
	fields := strings.Split(text, "|")
	if len(fields) == 5 {
		if codec.Questions {
			return Seat{}, errAt(line, "у брейна игрок пишется в самом вопросе, а не пятым полем")
		}
		if codec.Individual {
			return Seat{}, errAt(line, "в личной игре игрок не пишется — участник и есть игрок")
		}
	} else if len(fields) != 4 {
		return Seat{}, errAt(line, "место в бою — «кто | метки | Σ | место», а не %q", text)
	}
	seat := Seat{Name: strings.TrimSpace(fields[0]), Line: line}
	if seat.Name == "" {
		return Seat{}, errAt(line, "место без участника")
	}
	if codec.Questions {
		questions, err := parseQuestions(fields[1], seat.Name, line)
		if err != nil {
			return Seat{}, err
		}
		seat.Questions = questions
	} else if codec.Counts {
		counts, err := parseCounts(fields[1], seat.Name, line, codec.ThemeSize)
		if err != nil {
			return Seat{}, err
		}
		seat.Counts = counts
	} else {
		if strings.Contains(fields[1], ",") {
			return Seat{}, errAt(line, "у %s вопросы через запятую — так пишут брейн, а эта игра играет темы", seat.Name)
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
		if len(fields) == 5 {
			for _, name := range strings.Split(fields[4], ",") {
				name = strings.TrimSpace(name)
				if name == "-" {
					name = ""
				}
				seat.Players = append(seat.Players, name)
			}
			if len(seat.Players) != len(seat.Marks) {
				return Seat{}, errAt(line, "у %s тем %d, а игроков %d — игроки пишутся по одному на тему, «-» где лист никого не назвал",
					seat.Name, len(seat.Marks), len(seat.Players))
			}
		}
	}
	total, err := strconv.Atoi(strings.TrimSpace(fields[2]))
	if err != nil {
		return Seat{}, errAt(line, "Σ у %s — целое число, а не %q", seat.Name, strings.TrimSpace(fields[2]))
	}
	seat.Total = total
	placeText := strings.TrimSpace(fields[3])
	if placeText == "-" {
		seat.Unranked = true
		return seat, nil
	}
	if seat.Pinned = strings.HasSuffix(placeText, "!"); seat.Pinned {
		placeText = strings.TrimSpace(strings.TrimSuffix(placeText, "!"))
	}
	place, err := strconv.ParseFloat(placeText, 64)
	if err != nil || place <= 0 {
		return Seat{}, errAt(line, "место у %s — число от 1 (делённое место пишется как 1.5, поставленное вручную — как 3!, а не напечатанное листом — как -), а не %q",
			seat.Name, strings.TrimSpace(fields[3]))
	}
	seat.Place = place
	return seat, nil
}

// parseShootout reads `перестрелка Ктулху: 60` — the net points a shootout
// came to for one seat of the Match it sits in. Zero is refused rather than
// stored: a seat with no line nets zero already, so an explicit one is either a
// duplicate or a misread sheet.
func parseShootout(text string, line int, bout *Bout) error {
	name, points, ok := strings.Cut(strings.TrimPrefix(text, "перестрелка "), ":")
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return errAt(line, "перестрелка пишется как «перестрелка Ктулху: 60», а не %q", text)
	}
	value, err := strconv.Atoi(strings.TrimSpace(points))
	if err != nil || value == 0 {
		return errAt(line, "перестрелка у %s — ненулевое целое, а не %q", name, strings.TrimSpace(points))
	}
	for i := range bout.Seats {
		if bout.Seats[i].Name != name {
			continue
		}
		if bout.Seats[i].Shootout != 0 {
			return errAt(line, "перестрелка у %s записана дважды", name)
		}
		bout.Seats[i].Shootout = value
		return nil
	}
	return errAt(line, "перестрелка у %s, которого нет в бою %s", name, bout.At)
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

// parseOverride reads `override [s1/r2/w1/m1] место: причина`, or, scoped to
// one participant, `override [s1/r2/w1/m1] место Ктулху: причина`. Naming the
// participant matters: a Match-wide ruling hides the same field for everyone at
// the table, so a real defect in the other three seats would go unreported.
func parseOverride(text string, line int) (Override, error) {
	head, rest, err := splitHeader(strings.TrimSpace(strings.TrimPrefix(text, "override")), line)
	if err != nil {
		return Override{}, err
	}
	at, err := parseHead(head, line)
	if err != nil {
		return Override{}, err
	}
	subject, reason, ok := strings.Cut(rest, ":")
	subject, reason = strings.TrimSpace(subject), strings.TrimSpace(reason)
	if !ok || subject == "" || reason == "" {
		return Override{}, errAt(line,
			"расхождение пишется как «override [координата] поле [участник]: почему листу верить нельзя» — без причины это молча спрятанная ошибка")
	}
	field, participant, _ := strings.Cut(subject, " ")
	return Override{At: at, Field: field, Participant: strings.TrimSpace(participant), Reason: reason, Line: line}, nil
}

// parseCounts reads a Troika seat's middle field: one group per theme, one
// digit per question — how many of the three answered it — and «.» for a question
// nobody took. `131 ..1` reads: one took the first question, three the second,
// one the third; in the next theme only the third, and one took it.
func parseCounts(field, who string, line, size int) ([][]int, error) {
	if size <= 0 {
		size = 3
	}
	var out [][]int
	for _, theme := range strings.Fields(field) {
		if len(theme) != size {
			return nil, errAt(line, "у %s тема из %d вопросов, а написано %q", who, size, theme)
		}
		counts := make([]int, 0, size)
		for _, r := range theme {
			switch {
			case r == '.' || r == '-':
				counts = append(counts, 0)
			case r >= '0' && r <= '3':
				counts = append(counts, int(r-'0'))
			default:
				return nil, errAt(line, "у %s в теме %q — жду 0..3 или «.», сколько из троих ответили верно", who, theme)
			}
		}
		out = append(out, counts)
	}
	if len(out) == 0 {
		return nil, errAt(line, "у %s нет ни одной темы", who)
	}
	return out, nil
}

// parseQuestions reads a brain seat's middle field: one entry per question,
// comma-separated because a player's name has a space in the middle of it.
// Each is `-`, or a mark with the player who took it — `R Виктория Корнеева` —
// or a bare mark where the sheet did not record who buzzed.
func parseQuestions(field, who string, line int) ([]Answer, error) {
	var out []Answer
	for _, entry := range strings.Split(field, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, errAt(line, "у %s пустой вопрос — незаданный пишется как -", who)
		}
		if entry == "-" {
			out = append(out, Answer{})
			continue
		}
		mark, player, _ := strings.Cut(entry, " ")
		answer := Answer{Player: strings.TrimSpace(player)}
		switch mark {
		case "R":
			answer.Mark = Right
		case "W":
			answer.Mark = Wrong
		default:
			return nil, errAt(line, "вопрос у %s начинается с R, W или -, а не с %q", who, mark)
		}
		out = append(out, answer)
	}
	if len(out) == 0 {
		return nil, errAt(line, "у %s нет ни одного вопроса", who)
	}
	return out, nil
}
