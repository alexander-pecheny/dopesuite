package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	dopestrings "dope/i18nstrings"
)

func LoadContested(ctx context.Context, q Queryer, gameID int64) ([]ContestedAnswer, error) {
	list, err := CollectRows(ctx, q, `
select c.question, coalesce(gp.number, coalesce(p.number, 0)), c.answer, c.accepted_here
from od_contested c
join participants p on p.id = c.participant_id
left join game_participants gp on gp.game_id = c.game_id and gp.participant_id = c.participant_id
where c.game_id = ?`, []any{gameID}, func(rows *sql.Rows) (ContestedAnswer, error) {
		var a ContestedAnswer
		var accepted int
		err := rows.Scan(&a.Question, &a.Number, &a.Answer, &accepted)
		a.AcceptedHere = accepted == 1
		return a, err
	})
	if err != nil {
		return nil, err
	}
	SortContested(list)
	return list, nil
}

var ErrNoSuchNumber = errors.New(dopestrings.Default.Od.Contested.NoSuchNumber())

// participantByGameNumber resolves a Game's team Number to the Participant it
// seats — game_participants first, since a Number belongs to a Participant's
// entry in a Game, then the fest's registry for a Game that seats nobody yet.
func participantByGameNumber(ctx context.Context, q Queryer, festID, gameID, number int64) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx,
		`select participant_id from game_participants where game_id = ? and number = ?`, gameID, number).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	err = q.QueryRowContext(ctx, `
select id from participants
where fest_id = ? and coalesce(game_id, 0) in (0, ?) and roster = 'team' and number = ?
order by coalesce(game_id, 0) desc limit 1`, festID, gameID, number).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNoSuchNumber
	}
	return id, err
}

func SaveContestedTx(ctx context.Context, tx *sql.Tx, festID, gameID, userID int64, question int, number int64, answer, now string) error {
	participantID, err := participantByGameNumber(ctx, tx, festID, gameID, number)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
insert into od_contested(game_id, question, participant_id, answer, accepted_here, created_by, created_at)
values(?, ?, ?, ?, 0, ?, ?)
on conflict(game_id, question, participant_id) do update set answer = excluded.answer`,
		gameID, question, participantID, answer, NullableID(userID), now)
	return err
}

func SetContestedAcceptedTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, question int, number int64, accepted bool) error {
	participantID, err := participantByGameNumber(ctx, tx, festID, gameID, number)
	if err != nil {
		return err
	}
	value := 0
	if accepted {
		value = 1
	}
	_, err = tx.ExecContext(ctx, `
update od_contested set accepted_here = ? where game_id = ? and question = ? and participant_id = ?`,
		value, gameID, question, participantID)
	return err
}

func DeleteContestedTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, question int, number int64) error {
	participantID, err := participantByGameNumber(ctx, tx, festID, gameID, number)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`delete from od_contested where game_id = ? and question = ? and participant_id = ?`,
		gameID, question, participantID)
	return err
}

// Contested answer (CONTEXT.md): an answer near enough to the accepted one that the
// tournament's jury must rule on it after the game. dope stores them beside
// the OD document rather than in it — the ruling is not a score — and splices
// them into the document every reader gets.

type ContestedAnswer struct {
	Question     int    `json:"question"`
	Number       int64  `json:"number"`
	Answer       string `json:"answer"`
	AcceptedHere bool   `json:"acceptedHere"`
}

const ContestedKey = "contested"

func SortContested(list []ContestedAnswer) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Question != list[j].Question {
			return list[i].Question < list[j].Question
		}
		return list[i].Number < list[j].Number
	})
}

func WithContestedFor(ctx context.Context, q Queryer, gameID int64, state []byte) []byte {
	list, err := LoadContested(ctx, q, gameID)
	if err != nil || len(list) == 0 {
		return state
	}
	return WithContested(state, list)
}

// WithContested returns the OD document with the contested answers spliced in. A
// document that is not a JSON object is returned unchanged, so a broken state
// never costs a page its contested answers and vice versa.
func WithContested(state []byte, list []ContestedAnswer) []byte {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(state, &obj); err != nil {
		return state
	}
	if list == nil {
		list = []ContestedAnswer{}
	}
	SortContested(list)
	encoded, err := json.Marshal(list)
	if err != nil {
		return state
	}
	obj[ContestedKey] = encoded
	merged, err := json.Marshal(obj)
	if err != nil {
		return state
	}
	return merged
}

func StripContested(state []byte) []byte {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(state, &obj); err != nil {
		return state
	}
	if _, present := obj[ContestedKey]; !present {
		return state
	}
	delete(obj, ContestedKey)
	stripped, err := json.Marshal(obj)
	if err != nil {
		return state
	}
	return stripped
}

func ContestedAnswersFor(list []ContestedAnswer) map[int]map[int64]string {
	out := map[int]map[int64]string{}
	for _, c := range list {
		if out[c.Question] == nil {
			out[c.Question] = map[int64]string{}
		}
		out[c.Question][c.Number] = c.Answer
	}
	return out
}
