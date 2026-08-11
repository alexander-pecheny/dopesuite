package tests

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"

	"dope/dope/domain/replay"
	"dope/dope/domain/resolver"
	dopeserver "dope/dope/server"
)

// serverGame drives a real dope game from a transcript: it resolves a
// coordinate through the grain columns, writes Draws into the Edges, enters
// marks and closes бои over the same HTTP handlers a host uses, and reads back
// what the scorer made of it.
//
// It talks HTTP rather than calling the engine so that the replay exercises
// authorisation, the write-tx discipline and the resolver exactly as a live
// tournament does. Anything it can prove, a host could have done.
type serverGame struct {
	t        *testing.T
	srv      *dopeserver.Server
	festID   int64
	gameID   int64
	gameType string
	token    string
}

func (g *serverGame) db() *sql.DB { return g.srv.Eng().DB }

// matchAt turns a coordinate into a бой. The columns are the whole point:
// before them this had to be guessed out of a code string like `s1-g7`.
//
// The заход is read from the бой where it has one and from the stage otherwise.
// A stage is normally a Wave, but a Group holds one стол across all of them, so
// there the Wave is a property of the бой — as the Round already is.
func (g *serverGame) matchAt(at replay.Coord) (int64, string, error) {
	var id int64
	var code string
	err := g.db().QueryRow(`
select m.id, m.code from matches m
join stages s on s.id = m.stage_id
where s.game_id = ? and s.block_code = ? and s.group_code = ?
  and coalesce(nullif(m.wave, 0), s.wave_index) = ?
  and m.round = ?
order by m.position
limit 1 offset ?`,
		g.gameID, at.Block, at.Group, at.Wave, at.Round, at.Match-1).Scan(&id, &code)
	if err == sql.ErrNoRows {
		return 0, "", fmt.Errorf("в игре нет боя по координате %s", at)
	}
	return id, code, err
}

// Seat writes a Draw. A seat whose occupant has already played somewhere is
// recorded as the Edge it really is — «место N в бою X» — because the resolver
// re-seats every slot from its source whenever an earlier round recomputes, so a
// participant written straight into the slot would be overwritten by the next
// recompute. A seat with no history is a seed.
func (g *serverGame) Seat(at replay.Coord, names []string) error {
	matchID, _, err := g.matchAt(at)
	if err != nil {
		return err
	}
	for index, name := range names {
		participantID, err := g.participantID(name)
		if err != nil {
			return err
		}
		source, place, found, err := g.lastResult(participantID)
		if err != nil {
			return err
		}
		if found {
			ref := fmt.Sprintf(`{"match":%q,"place":%d,"label":"%s, м. %d"}`, source, place, source, place)
			_, err = g.db().Exec(`
update match_slots set source_type = 'from_match', source_ref_json = ?, participant_id = null
where match_id = ? and slot_index = ?`, ref, matchID, index)
		} else {
			_, err = g.db().Exec(`
update match_slots set source_type = 'seed', participant_id = ?
where match_id = ? and slot_index = ?`, participantID, matchID, index)
		}
		if err != nil {
			return err
		}
	}
	return g.resolve()
}

// resolve runs the same pass the server runs after any write that can move a
// Participant. Writing an Edge only says where a seat comes from; it is the
// resolver that puts somebody in it.
func (g *serverGame) resolve() error {
	tx, err := g.db().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := resolver.ResolveGameSlotsAndReseedsTx(g.t.Context(), tx, g.gameID); err != nil {
		return fmt.Errorf("резольвер: %w", err)
	}
	return tx.Commit()
}

// lastResult finds where a participant last finished, so a hand draw can be
// expressed as an Edge rather than as a seat the resolver will overwrite.
func (g *serverGame) lastResult(participantID int64) (string, int, bool, error) {
	var code string
	var place float64
	err := g.db().QueryRow(`
select m.code, r.place from match_results r
join matches m on m.id = r.match_id
where r.participant_id = ? and m.game_id = ? and m.status = 'finished'
order by m.position desc, m.id desc limit 1`, participantID, g.gameID).Scan(&code, &place)
	if err == sql.ErrNoRows {
		return "", 0, false, nil
	}
	return code, int(place), err == nil, err
}

func (g *serverGame) Seats(at replay.Coord) ([]string, error) {
	matchID, _, err := g.matchAt(at)
	if err != nil {
		return nil, err
	}
	rows, err := g.db().Query(`
select coalesce(p.name, '') from match_slots ms
left join participants p on p.id = ms.participant_id
where ms.match_id = ? order by ms.slot_index`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names, rows.Err()
}

// Play enters one participant's side of a бой as state patches — the same ops
// the host page sends when a judge taps a cell: the theme's player from the
// team's состав, then the marks.
func (g *serverGame) Play(at replay.Coord, name string, play replay.Play) error {
	if len(play.Questions) > 0 {
		return g.playBrain(at, name, play.Questions)
	}
	_, code, err := g.matchAt(at)
	if err != nil {
		return err
	}
	participantID, err := g.participantID(name)
	if err != nil {
		return err
	}
	var ops []map[string]any
	for theme, player := range play.Players {
		if player == "" {
			continue
		}
		playerID, err := g.rosterPlayerID(participantID, player)
		if err != nil {
			return fmt.Errorf("тема %d у %s: %w", theme+1, name, err)
		}
		ops = append(ops, map[string]any{
			"path":  []any{"participants", fmt.Sprint(participantID), "themes", theme, "player"},
			"value": playerID,
		})
	}
	for theme, answers := range play.Themes {
		for index, mark := range answers {
			value := ""
			switch mark {
			case replay.Right:
				value = "right"
			case replay.Wrong:
				value = "wrong"
			default:
				continue
			}
			ops = append(ops, map[string]any{
				"path":  []any{"participants", fmt.Sprint(participantID), "themes", theme, "answers", index},
				"value": value,
			})
		}
	}
	if play.Shootout != 0 {
		shootout, err := g.shootoutOps(at, participantID, play.Shootout)
		if err != nil {
			return fmt.Errorf("перестрелка у %s: %w", name, err)
		}
		ops = append(ops, shootout...)
	}
	if len(ops) == 0 {
		return nil
	}
	resp := scopedAPIRequest(g.t, g.srv, http.MethodPatch,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/state", g.festID, g.gameID, code),
		map[string]any{"ops": ops}, g.token)
	if resp.Code != http.StatusOK {
		return fmt.Errorf("отметки %s: %d %s", code, resp.Code, resp.Body.String())
	}
	return nil
}

// shootoutOps writes one seat's перестрелка into the blob's shootout theme: the
// sheet kept a net total, so the marks are synthesized — greedily, distinct
// nominals from 50 down — faithful in total, invented in composition. Every
// seated participant gets the theme (the live host page keeps the grid in
// lockstep the same way), but only ones that do not have it yet, so an earlier
// seat's marks in the same бой survive.
func (g *serverGame) shootoutOps(at replay.Coord, participantID int64, net int) ([]map[string]any, error) {
	matchID, _, err := g.matchAt(at)
	if err != nil {
		return nil, err
	}
	var state string
	if err := g.db().QueryRow(`
select coalesce(state_json, '{}') from matches where id = ?`, matchID).Scan(&state); err != nil {
		return nil, err
	}
	var blob struct {
		Participants map[string]struct {
			ShootoutThemes []any `json:"shootoutThemes"`
		} `json:"participants"`
	}
	if err := json.Unmarshal([]byte(state), &blob); err != nil {
		return nil, err
	}
	themes := shootoutMarks(net)
	check := 0
	for _, theme := range themes {
		for index, mark := range theme {
			if mark == "right" {
				check += (index + 1) * 10
			} else if mark == "wrong" {
				check -= (index + 1) * 10
			}
		}
	}
	if check != net {
		return nil, fmt.Errorf("нетто %d не раскладывается по номиналам", net)
	}
	var ops []map[string]any
	rows, err := g.db().Query(`
select participant_id from match_slots where match_id = ? and participant_id is not null order by slot_index`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		have := len(blob.Participants[fmt.Sprint(id)].ShootoutThemes)
		for t := have; t < len(themes); t++ {
			ops = append(ops, map[string]any{
				"path":  []any{"participants", fmt.Sprint(id), "shootoutThemes", t},
				"value": map[string]any{"answers": []any{"", "", "", "", ""}},
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for t, theme := range themes {
		for index, mark := range theme {
			if mark == "" {
				continue
			}
			ops = append(ops, map[string]any{
				"path":  []any{"participants", fmt.Sprint(participantID), "shootoutThemes", t, "answers", index},
				"value": mark,
			})
		}
	}
	return ops, nil
}

// shootoutMarks decomposes a net total into theme marks on the 10..50 scale:
// one right (or, negative, wrong) per nominal, highest first, a further theme
// if ±150 per theme is not enough.
func shootoutMarks(net int) [][5]string {
	sign, remaining := "right", net
	if net < 0 {
		sign, remaining = "wrong", -net
	}
	var themes [][5]string
	for remaining > 0 {
		var theme [5]string
		for index := 4; index >= 0; index-- {
			value := (index + 1) * 10
			if remaining >= value {
				theme[index] = sign
				remaining -= value
			}
		}
		if theme == ([5]string{}) {
			break
		}
		themes = append(themes, theme)
	}
	return themes
}

func (g *serverGame) Finish(at replay.Coord) error {
	_, code, err := g.matchAt(at)
	if err != nil {
		return err
	}
	resp := scopedAPIRequest(g.t, g.srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/finish", g.festID, g.gameID, code),
		map[string]any{"finished": true}, g.token)
	if resp.Code != http.StatusOK {
		return fmt.Errorf("закрытие %s: %d %s", code, resp.Code, resp.Body.String())
	}
	return g.calculateReadyReseeds()
}

// calculateReadyReseeds does what a host does after closing the last бой of a
// round: presses «рассчитать пересев». Finishing a бой only invalidates a
// reseed — the ranking is recomputed on the host's word, not silently — so a
// replay that never pressed the button would find the next round unseated and
// blame the resolver.
//
// A stage that is not ready yet answers 400 and is simply left for later, which
// is the same thing as the button being greyed out.
func (g *serverGame) calculateReadyReseeds() error {
	rows, err := g.db().Query(`
select code from stages where game_id = ? and stage_type = 'reseed' order by position`, g.gameID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return err
		}
		codes = append(codes, code)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, code := range codes {
		resp := scopedAPIRequest(g.t, g.srv, http.MethodPost,
			fmt.Sprintf("/api/fest/%d/games/%d/stages/%s/reseed", g.festID, g.gameID, code), nil, g.token)
		if resp.Code != http.StatusOK && resp.Code != http.StatusBadRequest {
			return fmt.Errorf("пересев %s: %d %s", code, resp.Code, resp.Body.String())
		}
	}
	return nil
}

func (g *serverGame) Outcome(at replay.Coord) (map[string]replay.Result, error) {
	matchID, _, err := g.matchAt(at)
	if err != nil {
		return nil, err
	}
	// Σ is whatever the sheet printed as the бой's score, and that is not the
	// same column in every game: ЭК and своя игра score points, брейн counts the
	// questions a side took.
	score := "r.total"
	if g.gameType == "brain" {
		score = "coalesce(cast(r.metrics_json ->> '$.taken' as integer), 0)"
	}
	rows, err := g.db().Query(`
select p.name, r.place, `+score+` from match_results r
join participants p on p.id = r.participant_id
where r.match_id = ?`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]replay.Result{}
	for rows.Next() {
		var name string
		var place float64
		var total int
		if err := rows.Scan(&name, &place, &total); err != nil {
			return nil, err
		}
		out[name] = replay.Result{Place: place, Total: total}
	}
	return out, rows.Err()
}

// Lineups writes the game's составы the way the scheme import does: players
// into the fest pool, membership into participant_players, so the state patch
// that names a theme's player finds him in the team's roster. Two games of one
// фест share their participants, so a player another game already rostered is
// kept rather than doubled.
func (g *serverGame) Lineups(lineups []replay.Lineup) error {
	for _, lineup := range lineups {
		teamID, err := g.participantID(lineup.Team)
		if err != nil {
			return err
		}
		var next int
		if err := g.db().QueryRow(`
select count(*) from participant_players where participant_id = ?`, teamID).Scan(&next); err != nil {
			return err
		}
		for _, player := range lineup.Players {
			if _, err := g.rosterPlayerID(teamID, player); err == nil {
				continue
			}
			first, last, _ := strings.Cut(player, " ")
			res, err := g.db().Exec(`
insert into players(fest_id, first_name, last_name) values(?, ?, ?)`, g.festID, first, last)
			if err != nil {
				return err
			}
			playerID, err := res.LastInsertId()
			if err != nil {
				return err
			}
			if _, err := g.db().Exec(`
insert into participant_players(participant_id, player_id, roster_order)
values(?, ?, ?)`, teamID, playerID, next); err != nil {
				return err
			}
			next++
		}
	}
	return nil
}

// rosterPlayerID resolves a player's display name inside one team's roster —
// the same list the patch handler validates theme players against.
func (g *serverGame) rosterPlayerID(participantID int64, name string) (int64, error) {
	var id int64
	err := g.db().QueryRow(`
select p.id from players p
join participant_players tp on tp.player_id = p.id
where tp.participant_id = ? and trim(p.first_name || ' ' || p.last_name) = ?`,
		participantID, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("игрока %q нет в составе", name)
	}
	return id, err
}

func (g *serverGame) participantID(name string) (int64, error) {
	var id int64
	err := g.db().QueryRow(`
select id from participants where fest_id = ? and name = ?`, g.festID, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("в фесте нет участника %q", name)
	}
	return id, err
}

// PlayerStats aggregates the game's finished бои per player, in the same three
// columns the transcript's [статистика] carries: ЭК — Σ, positive themes,
// themes played; брейн — попытки, верно, неверно over the regular questions;
// личная СИ — Σ, Σ+ and бои, the participant being the player.
func (g *serverGame) PlayerStats() ([]replay.Stat, error) {
	switch g.gameType {
	case "brain":
		return g.brainStats()
	case "ek":
		return g.ekStats()
	}
	return g.individualStats()
}

// finishedStates streams every finished бой's state with its seated
// participants' names by id (slot order for брейн's index-aligned sides).
func (g *serverGame) finishedStates(walk func(state string, seated []string, names map[int64]string) error) error {
	rows, err := g.db().Query(`
select m.id, coalesce(m.state_json, '{}') from matches m
where m.game_id = ? and m.status = 'finished' order by m.position, m.id`, g.gameID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type finished struct {
		id    int64
		state string
	}
	var matches []finished
	for rows.Next() {
		var m finished
		if err := rows.Scan(&m.id, &m.state); err != nil {
			return err
		}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, m := range matches {
		slots, err := g.db().Query(`
select ms.slot_index, ms.participant_id, coalesce(p.name, '')
from match_slots ms left join participants p on p.id = ms.participant_id
where ms.match_id = ? order by ms.slot_index`, m.id)
		if err != nil {
			return err
		}
		var seated []string
		names := map[int64]string{}
		for slots.Next() {
			var index int
			var pid sql.NullInt64
			var name string
			if err := slots.Scan(&index, &pid, &name); err != nil {
				slots.Close()
				return err
			}
			seated = append(seated, name)
			if pid.Valid {
				names[pid.Int64] = name
			}
		}
		slots.Close()
		if err := slots.Err(); err != nil {
			return err
		}
		if err := walk(m.state, seated, names); err != nil {
			return err
		}
	}
	return nil
}

// statRows folds an accumulation map into sorted Stat lines.
func statRows(acc map[[2]string]*[3]int) []replay.Stat {
	out := make([]replay.Stat, 0, len(acc))
	for key, values := range acc {
		out = append(out, replay.Stat{Player: key[0], Team: key[1], Values: *values})
	}
	sort.Slice(out, func(a, b int) bool {
		return out[a].Player+"\x1f"+out[a].Team < out[b].Player+"\x1f"+out[b].Team
	})
	return out
}

func (g *serverGame) ekStats() ([]replay.Stat, error) {
	playerName := map[int64]string{}
	rows, err := g.db().Query(`select id, trim(first_name || ' ' || last_name) from players where fest_id = ?`, g.festID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		playerName[id] = name
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := []int{10, 20, 30, 40, 50}
	acc := map[[2]string]*[3]int{}
	err = g.finishedStates(func(state string, _ []string, names map[int64]string) error {
		var blob struct {
			Participants map[string]struct {
				Themes []struct {
					Player  int64     `json:"player"`
					Answers [5]string `json:"answers"`
				} `json:"themes"`
			} `json:"participants"`
		}
		if err := json.Unmarshal([]byte(state), &blob); err != nil {
			return err
		}
		for pid, section := range blob.Participants {
			id, err := strconv.ParseInt(pid, 10, 64)
			if err != nil {
				return err
			}
			team := names[id]
			for _, theme := range section.Themes {
				if theme.Player == 0 {
					continue
				}
				key := [2]string{playerName[theme.Player], team}
				entry := acc[key]
				if entry == nil {
					entry = &[3]int{}
					acc[key] = entry
				}
				sum := 0
				for i, mark := range theme.Answers {
					if mark == "right" {
						sum += values[i]
					} else if mark == "wrong" {
						sum -= values[i]
					}
				}
				entry[0] += sum
				if sum > 0 {
					entry[1]++
				}
				entry[2]++
			}
		}
		return nil
	})
	return statRows(acc), err
}

func (g *serverGame) brainStats() ([]replay.Stat, error) {
	acc := map[[2]string]*[3]int{}
	err := g.finishedStates(func(state string, seated []string, _ map[int64]string) error {
		var blob struct {
			Teams []struct {
				Rows []struct {
					Player string `json:"player"`
					Mark   string `json:"mark"`
				} `json:"rows"`
			} `json:"teams"`
			Tiebreaks int `json:"tiebreaks"`
		}
		if err := json.Unmarshal([]byte(state), &blob); err != nil {
			return err
		}
		for side, team := range blob.Teams {
			if side >= len(seated) {
				break
			}
			regular := len(team.Rows) - blob.Tiebreaks
			for index, row := range team.Rows {
				if index >= regular || row.Player == "" || row.Mark == "" {
					continue
				}
				key := [2]string{row.Player, seated[side]}
				entry := acc[key]
				if entry == nil {
					entry = &[3]int{}
					acc[key] = entry
				}
				entry[0]++
				if row.Mark == "right" {
					entry[1]++
				} else {
					entry[2]++
				}
			}
		}
		return nil
	})
	return statRows(acc), err
}

func (g *serverGame) individualStats() ([]replay.Stat, error) {
	values := []int{10, 20, 30, 40, 50}
	acc := map[[2]string]*[3]int{}
	entryFor := func(player string) *[3]int {
		key := [2]string{player, ""}
		entry := acc[key]
		if entry == nil {
			entry = &[3]int{}
			acc[key] = entry
		}
		return entry
	}
	err := g.finishedStates(func(state string, seated []string, names map[int64]string) error {
		// Бои are counted from the seating: a player who sat a бой and took
		// nothing has no state section, and the sheet still counts the бой.
		for _, name := range seated {
			if name != "" {
				entryFor(name)[2]++
			}
		}
		var blob struct {
			Participants map[string]struct {
				Themes []struct {
					Answers [5]string `json:"answers"`
				} `json:"themes"`
			} `json:"participants"`
		}
		if err := json.Unmarshal([]byte(state), &blob); err != nil {
			return err
		}
		for pid, section := range blob.Participants {
			id, err := strconv.ParseInt(pid, 10, 64)
			if err != nil {
				return err
			}
			entry := entryFor(names[id])
			for _, theme := range section.Themes {
				for i, mark := range theme.Answers {
					if mark == "right" {
						entry[0] += values[i]
						entry[1] += values[i]
					} else if mark == "wrong" {
						entry[0] -= values[i]
					}
				}
			}
		}
		return nil
	})
	return statRows(acc), err
}

// Pin writes a place the hosts set by hand. It is Protocol state (ADR-0005), so
// it travels the same patch path a mark does.
func (g *serverGame) Pin(at replay.Coord, name string, place float64) error {
	_, code, err := g.matchAt(at)
	if err != nil {
		return err
	}
	participantID, err := g.participantID(name)
	if err != nil {
		return err
	}
	resp := scopedAPIRequest(g.t, g.srv, http.MethodPatch,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/state", g.festID, g.gameID, code),
		map[string]any{"ops": []map[string]any{{
			"path":  []any{"participants", fmt.Sprint(participantID), "pin"},
			"value": place,
		}}}, g.token)
	if resp.Code != http.StatusOK {
		return fmt.Errorf("место вручную %s: %d %s", code, resp.Code, resp.Body.String())
	}
	return nil
}

// playBrain enters a брейн side. Its state is not a per-participant blob but two
// index-aligned columns of rows, so the seat has to be found first: which side
// of this бой the participant sits on is the Structure's knowledge, and the only
// place it is written down is the slot order.
//
// Questions past the бой's own are перестрелка — the sheet's «П» rows. They are
// appended to both sides, exactly as the «+ П» button does, and setting the same
// index twice is harmless because every op is an assignment.
func (g *serverGame) playBrain(at replay.Coord, name string, answers []replay.Answer) error {
	matchID, code, err := g.matchAt(at)
	if err != nil {
		return err
	}
	participantID, err := g.participantID(name)
	if err != nil {
		return err
	}
	var side int
	err = g.db().QueryRow(`
select slot_index from match_slots where match_id = ? and participant_id = ?`, matchID, participantID).Scan(&side)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%s не сидит в бою %s", name, code)
	}
	if err != nil {
		return err
	}
	var rows int
	if err := g.db().QueryRow(`
select json_array_length(state_json -> '$.teams[0].rows') from matches where id = ?`, matchID).Scan(&rows); err != nil {
		return err
	}
	var ops []map[string]any
	for index := rows; index < len(answers); index++ {
		ops = append(ops,
			map[string]any{"path": []any{"tiebreaks"}, "value": index + 1 - rows},
			map[string]any{"path": []any{"teams", 0, "rows", index}, "value": map[string]any{"player": "", "mark": ""}},
			map[string]any{"path": []any{"teams", 1, "rows", index}, "value": map[string]any{"player": "", "mark": ""}})
	}
	for index, answer := range answers {
		mark := ""
		switch answer.Mark {
		case replay.Right:
			mark = "right"
		case replay.Wrong:
			mark = "wrong"
		}
		if mark == "" && answer.Player == "" {
			continue
		}
		ops = append(ops, map[string]any{"path": []any{"teams", side, "rows", index, "mark"}, "value": mark})
		if answer.Player != "" {
			ops = append(ops, map[string]any{"path": []any{"teams", side, "rows", index, "player"}, "value": answer.Player})
		}
	}
	if len(ops) == 0 {
		return nil
	}
	resp := scopedAPIRequest(g.t, g.srv, http.MethodPatch,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/state", g.festID, g.gameID, code),
		map[string]any{"ops": ops}, g.token)
	if resp.Code != http.StatusOK {
		return fmt.Errorf("вопросы %s: %d %s", code, resp.Code, resp.Body.String())
	}
	return nil
}
