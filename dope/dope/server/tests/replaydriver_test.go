package tests

import (
	"database/sql"
	"fmt"
	"net/http"
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
	t      *testing.T
	srv    *dopeserver.Server
	festID int64
	gameID int64
	token  string
}

func (g *serverGame) db() *sql.DB { return g.srv.Eng().DB }

// matchAt turns a coordinate into a бой. The four columns are the whole point:
// before them this had to be guessed out of a code string like `s1-g7`.
func (g *serverGame) matchAt(at replay.Coord) (int64, string, error) {
	var id int64
	var code string
	err := g.db().QueryRow(`
select m.id, m.code from matches m
join stages s on s.id = m.stage_id
where s.game_id = ? and s.block_code = ? and s.wave_index = ? and s.group_code = ?
  and m.round = ?
order by m.position
limit 1 offset ?`,
		g.gameID, at.Block, at.Wave, at.Group, at.Round, at.Match-1).Scan(&id, &code)
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

// Play enters one participant's marks as state patches — the same ops the host
// page sends when a judge taps a cell.
func (g *serverGame) Play(at replay.Coord, name string, marks [][5]replay.Mark) error {
	_, code, err := g.matchAt(at)
	if err != nil {
		return err
	}
	participantID, err := g.participantID(name)
	if err != nil {
		return err
	}
	var ops []map[string]any
	for theme, answers := range marks {
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
	return nil
}

func (g *serverGame) Outcome(at replay.Coord) (map[string]replay.Result, error) {
	matchID, _, err := g.matchAt(at)
	if err != nil {
		return nil, err
	}
	rows, err := g.db().Query(`
select p.name, r.place, r.total from match_results r
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

func (g *serverGame) participantID(name string) (int64, error) {
	var id int64
	err := g.db().QueryRow(`
select id from participants where fest_id = ? and name = ?`, g.festID, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("в фесте нет участника %q", name)
	}
	return id, err
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
