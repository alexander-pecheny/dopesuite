package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"dope/dope/platform/util"
)

// Брейн бой storage. A брейн match is a head-to-head between two teams over K
// questions; each question, per team, records the answering player and a mark
// (took it = "right", missed = "wrong", untouched = ""). This is its own entity
// — брейн has no themes — so it lives in its own match_questions table rather
// than borrowing EK's themes/answers. The bracket around it (stages, matches,
// slots, match_results, reseed) is shared with EK.

// StageLayoutConfig parses a stage's config_json (as persisted by
// storeutil.StageConfigJSON, which nests the scheme stage config under "config")
// and returns its layout marker and questions-per-бой. Layout "crosstable" marks
// a round-robin group of head-to-head бои.
func StageLayoutConfig(configJSON string) (layout string, questions int) {
	if configJSON == "" {
		return "", 0
	}
	var outer struct {
		Config struct {
			Layout    string `json:"layout"`
			Questions int    `json:"questions"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(configJSON), &outer); err != nil {
		return "", 0
	}
	return outer.Config.Layout, outer.Config.Questions
}

// BrainMatchLayout is the stage config layout value marking a брейн round-robin
// group (its бои are head-to-head and rendered as a cross-table).
const BrainMatchLayout = "crosstable"

// BrainQuestion is one team's cell for one question of a бой.
type BrainQuestion struct {
	Player string `json:"player"`
	Mark   string `json:"mark"` // "right" | "wrong" | ""
}

// BrainRosterPlayer is one selectable player for the бой protocol's player picker.
type BrainRosterPlayer struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// BrainTeamBout is one team's side of a бой: its identity plus a mark/player per
// question, index-aligned with the бой's question list.
type BrainTeamBout struct {
	TeamID    int64               `json:"teamID"`
	Name      string              `json:"name"`
	Roster    []BrainRosterPlayer `json:"roster"`
	Place     int                 `json:"place"`
	Questions []BrainQuestion     `json:"questions"`
}

// BrainMatch is a бой's full state: header plus the two teams' per-question grids.
type BrainMatch struct {
	MatchID       int64           `json:"-"`
	GameID        int64           `json:"-"`
	Code          string          `json:"code"`
	Title         string          `json:"title"`
	Finished      bool            `json:"finished"`
	Revision      int64           `json:"revision"`
	Venue         *VenueView      `json:"venue,omitempty"`
	QuestionCount int             `json:"questionCount"`
	Teams         []BrainTeamBout `json:"teams"`
}

// BrainTaken counts the questions a team took: marks normalised to "right".
func BrainTaken(team BrainTeamBout) int {
	taken := 0
	for _, q := range team.Questions {
		if NormalizeMark(q.Mark) == "right" {
			taken++
		}
	}
	return taken
}

// BrainMatchPoints is the head-to-head бой rule: the side taking more questions
// gets 2 group points (О), a tie 1 each, the loser 0. Kept here (not in the
// games domain) so the store-level recompute can call it without an upward
// import; the games package re-exports the same rule for its pure helpers.
func BrainMatchPoints(takenA, takenB int) (int, int) {
	switch {
	case takenA > takenB:
		return 2, 0
	case takenA < takenB:
		return 0, 2
	default:
		return 1, 1
	}
}

// LoadBrainMatch loads a бой by fest id and code: header, question count, and
// both teams with their rosters and per-question player/mark grids.
func LoadBrainMatch(ctx context.Context, q Queryer, festID int64, code string) (BrainMatch, error) {
	var match BrainMatch
	var status, configJSON, rosterSource string
	var venueNumber sql.NullInt64
	var venueTitle sql.NullString
	if err := q.QueryRowContext(ctx, `
select m.id, m.game_id, m.code, m.title, m.status, m.revision, s.config_json, v.number, v.title, g.roster_source
from matches m
join games g on g.id = m.game_id
join stages s on s.id = m.stage_id
left join venues v on v.id = m.venue_id
where m.fest_id = ? and m.code = ?`, festID, code).
		Scan(&match.MatchID, &match.GameID, &match.Code, &match.Title, &status, &match.Revision, &configJSON, &venueNumber, &venueTitle, &rosterSource); err != nil {
		return BrainMatch{}, err
	}
	match.Finished = status == "finished"
	if venueNumber.Valid {
		match.Venue = &VenueView{Number: int(venueNumber.Int64), Title: venueTitle.String}
	}
	_, match.QuestionCount = StageLayoutConfig(configJSON)
	if match.QuestionCount <= 0 {
		match.QuestionCount = 1
	}

	slots, err := CollectRows(ctx, q, `
select ms.slot_index, coalesce(ms.team_id, 0), coalesce(t.name, ''), coalesce(mr.place, 0), ms.source_type, ms.source_ref_json
from match_slots ms
left join teams t on t.id = ms.team_id
left join match_results mr on mr.match_id = ms.match_id and mr.team_id = ms.team_id
where ms.match_id = ?
order by ms.slot_index`, []any{match.MatchID}, func(rows *sql.Rows) (BrainTeamBout, error) {
		var team BrainTeamBout
		var place float64
		var sourceType, sourceRef string
		if err := rows.Scan(new(int), &team.TeamID, &team.Name, &place, &sourceType, &sourceRef); err != nil {
			return team, err
		}
		team.Place = int(place)
		if team.TeamID == 0 {
			team.Name = SlotSourceLabel(sourceType, sourceRef)
		}
		return team, nil
	})
	if err != nil {
		return BrainMatch{}, err
	}
	for i := range slots {
		team := &slots[i]
		team.Questions = make([]BrainQuestion, match.QuestionCount)
		if team.TeamID == 0 {
			continue
		}
		team.Roster, err = loadBrainRoster(ctx, q, match.GameID, rosterSource, team.TeamID)
		if err != nil {
			return BrainMatch{}, err
		}
		rows, err := CollectRows(ctx, q, `
select mq.question_index, mq.mark, coalesce(p.first_name, ''), coalesce(p.last_name, '')
from match_questions mq
left join players p on p.id = mq.player_id
where mq.match_id = ? and mq.team_id = ?`, []any{match.MatchID, team.TeamID}, func(rows *sql.Rows) (BrainQuestion, error) {
			var qn BrainQuestion
			var idx int
			var first, last string
			if err := rows.Scan(&idx, &qn.Mark, &first, &last); err != nil {
				return qn, err
			}
			qn.Player = JoinPlayerName(first, last)
			if idx >= 0 && idx < len(team.Questions) {
				team.Questions[idx] = BrainQuestion{Player: qn.Player, Mark: NormalizeMark(qn.Mark)}
			}
			return qn, nil
		})
		_ = rows
		if err != nil {
			return BrainMatch{}, err
		}
	}
	match.Teams = slots
	return match, nil
}

func loadBrainRoster(ctx context.Context, q Queryer, gameID int64, rosterSource string, teamID int64) ([]BrainRosterPlayer, error) {
	query := `
select p.id, p.first_name, p.last_name
from team_players tp join players p on p.id = tp.player_id
where tp.team_id = ? order by tp.roster_order`
	args := []any{teamID}
	if rosterSource == "game" {
		query = `
select p.id, p.first_name, p.last_name
from game_team_players gtp join players p on p.id = gtp.player_id
where gtp.game_id = ? and gtp.team_id = ? order by gtp.roster_order`
		args = []any{gameID, teamID}
	}
	return CollectRows(ctx, q, query, args, func(rows *sql.Rows) (BrainRosterPlayer, error) {
		var player BrainRosterPlayer
		var first, last string
		if err := rows.Scan(&player.ID, &first, &last); err != nil {
			return player, err
		}
		player.Name = JoinPlayerName(first, last)
		return player, nil
	})
}

// SetBrainQuestionMarkTx sets a team's mark for one question of a бой.
func SetBrainQuestionMarkTx(ctx context.Context, tx *sql.Tx, matchID, teamID int64, questionIndex int, mark string) error {
	_, err := tx.ExecContext(ctx, `
insert into match_questions(match_id, team_id, question_index, mark)
values(?, ?, ?, ?)
on conflict(match_id, team_id, question_index) do update set mark = excluded.mark`,
		matchID, teamID, questionIndex, NormalizeMark(mark))
	return err
}

// SetBrainQuestionPlayerTx sets the answering player for one question of a бой;
// playerID 0 clears it.
func SetBrainQuestionPlayerTx(ctx context.Context, tx *sql.Tx, matchID, teamID int64, questionIndex int, playerID int64) error {
	_, err := tx.ExecContext(ctx, `
insert into match_questions(match_id, team_id, question_index, player_id)
values(?, ?, ?, ?)
on conflict(match_id, team_id, question_index) do update set player_id = excluded.player_id`,
		matchID, teamID, questionIndex, util.NullableInt64(playerID))
	return err
}

// EnsureBrainQuestionsTx idempotently creates a team's K empty question rows for
// a бой, called when a team lands in the бой's slot (the брейн analogue of
// EnsureRegularThemes).
func EnsureBrainQuestionsTx(ctx context.Context, tx *sql.Tx, matchID, teamID int64, count int) error {
	for i := 0; i < count; i++ {
		if _, err := tx.ExecContext(ctx, `
insert into match_questions(match_id, team_id, question_index, mark)
values(?, ?, ?, '')
on conflict(match_id, team_id, question_index) do nothing`, matchID, teamID, i); err != nil {
			return err
		}
	}
	return nil
}

// RecalculateBrainMatchResultsTx recomputes and upserts the match_results rows
// for a бой from its question grids: total = questions taken, plus = О group
// points, place by comparison. Places materialise only once the бой is finished,
// so provisional standings never leak into downstream advancement.
func RecalculateBrainMatchResultsTx(ctx context.Context, tx *sql.Tx, match BrainMatch) error {
	taken := make([]int, len(match.Teams))
	for i, team := range match.Teams {
		taken[i] = BrainTaken(team)
	}
	for i, team := range match.Teams {
		if team.TeamID == 0 {
			continue
		}
		points, place, conceded := 0, 0, 0
		if len(match.Teams) == 2 {
			other := taken[1-i]
			conceded = other
			pa, _ := BrainMatchPoints(taken[i], other)
			points = pa
			switch {
			case taken[i] > other:
				place = 1
			case taken[i] < other:
				place = 2
			default:
				place = 1
			}
		}
		placeValue := 0.0
		if match.Finished {
			placeValue = float64(place)
		}
		metrics := util.MustJSON(map[string]any{"points": points, "conceded": conceded})
		if _, err := tx.ExecContext(ctx, `
insert into match_results(match_id, team_id, place, total, plus, tiebreak, metrics_json)
values(?, ?, ?, ?, ?, 0, ?)
on conflict(match_id, team_id) do update set
  place = excluded.place,
  total = excluded.total,
  plus = excluded.plus,
  tiebreak = excluded.tiebreak,
  metrics_json = excluded.metrics_json`, match.MatchID, team.TeamID, placeValue, taken[i], points, metrics); err != nil {
			return err
		}
	}
	return nil
}
