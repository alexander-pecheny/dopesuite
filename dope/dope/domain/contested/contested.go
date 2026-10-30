// Package contested keeps an OD's contested answers (CONTEXT.md): the answers a host
// recorded as disputed, and the jury's ruling on each. A ruling belongs to a
// question and an answer, not to a team, so every team that gave the same
// answer gets it. The text stays in these tables, readable by the hosts and
// the jury alone; the Game's document gets each contested answer without its text
// (games.ODContested), for the standings and for every viewer to see that one
// is pending.
package contested

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	"dope/dope/domain/flatgame"
	"dope/dope/domain/games"
	"dope/dope/domain/multifest"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// Answer is one team's contested answer, the team under the number the Game gives it.
type Answer struct {
	Question int    `json:"question"`
	Number   int64  `json:"number"`
	Answer   string `json:"answer"`
	Status   string `json:"status"`
}

// Enabled says whether the fest's OD takes contested answers: as the fest was set, or,
// when it was never set, only for a synch fest.
func Enabled(ctx context.Context, q store.Queryer, festID int64) (bool, error) {
	var on bool
	err := q.QueryRowContext(ctx, `select coalesce(contested, kind = 'synch') from fests where id = ?`, festID).Scan(&on)
	return on, err
}

// Key is what makes two answers one for a ruling: case and spacing aside.
func Key(answer string) string {
	return strings.ToLower(strings.Join(strings.Fields(answer), " "))
}

// List is every contested answer of the Game with its text and ruling, by question
// and then by team number.
func List(ctx context.Context, q store.Queryer, festID, gameID int64) ([]Answer, error) {
	numbers, err := teamNumbers(ctx, q, festID, gameID)
	if err != nil {
		return nil, err
	}
	type ruled struct {
		question int
		key      string
	}
	type ruling struct {
		ruled
		accepted bool
	}
	rows, err := store.CollectRows(ctx, q, `select question, answer_key, accepted from od_contested_rulings where game_id = ?`,
		[]any{gameID}, func(rs *sql.Rows) (ruling, error) {
			var r ruling
			return r, rs.Scan(&r.question, &r.key, &r.accepted)
		})
	if err != nil {
		return nil, err
	}
	rulings := make(map[ruled]bool, len(rows))
	for _, r := range rows {
		rulings[r.ruled] = r.accepted
	}
	type entry struct {
		Answer
		team int64
	}
	entries, err := store.CollectRows(ctx, q, `select question, fest_team_id, answer from od_contested where game_id = ?`,
		[]any{gameID}, func(rs *sql.Rows) (entry, error) {
			var e entry
			return e, rs.Scan(&e.Question, &e.team, &e.Answer.Answer)
		})
	if err != nil {
		return nil, err
	}
	out := make([]Answer, 0, len(entries))
	for _, e := range entries {
		number, ok := numbers[e.team]
		if !ok {
			continue
		}
		e.Number, e.Status = number, games.ContestedPending
		if accepted, ruled := rulings[ruled{e.Question, Key(e.Answer.Answer)}]; ruled {
			e.Status = games.ContestedRejected
			if accepted {
				e.Status = games.ContestedAccepted
			}
		}
		out = append(out, e.Answer)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Question != out[j].Question {
			return out[i].Question < out[j].Question
		}
		return out[i].Number < out[j].Number
	})
	return out, nil
}

// teamNumbers maps the fest teams the Game seats to the numbers it gives them.
func teamNumbers(ctx context.Context, q store.Queryer, festID, gameID int64) (map[int64]int64, error) {
	byNumber, err := gameTeams(ctx, q, festID, gameID)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(byNumber))
	for number, team := range byNumber {
		out[team.ID] = number
	}
	return out, nil
}

func gameTeams(ctx context.Context, q store.Queryer, festID, gameID int64) (map[int64]multifest.Team, error) {
	venue, err := multifest.GameVenue(ctx, q, gameID)
	if err != nil {
		return nil, err
	}
	return multifest.ByNumber(ctx, q, festID, venue)
}

// teamTx is the fest team the Game numbers so, if it plays at one of venues
// (any team for none).
func teamTx(ctx context.Context, tx *sql.Tx, festID, gameID, number int64, venues []int64) (multifest.Team, error) {
	teams, err := gameTeams(ctx, tx, festID, gameID)
	if err != nil {
		return multifest.Team{}, err
	}
	team, ok := teams[number]
	if !ok {
		return team, corei18n.User(dopestrings.Default.Od.Contested.NoSuchNumber())
	}
	if len(venues) == 0 {
		return team, nil
	}
	var venue int64
	if err := tx.QueryRowContext(ctx, `select coalesce(multifest_venue_id, 0) from fest_teams where id = ?`, team.ID).Scan(&venue); err != nil {
		return team, err
	}
	if !slices.Contains(venues, venue) {
		return team, ErrOtherVenue
	}
	return team, nil
}

// ErrOtherVenue refuses a host limited to venues a team of another venue.
var ErrOtherVenue = errors.New("contested: a team of another venue")

// SaveTx records the team's contested answer on the question, or changes its text. A
// host limited to venues names them, and may record only their teams'.
func SaveTx(ctx context.Context, tx *sql.Tx, festID, gameID, userID int64, venues []int64, question int, number int64, answer string) error {
	answer = strings.TrimSpace(answer)
	if answer == "" || question < 0 {
		return corei18n.User(dopestrings.Default.Od.Contested.AnswerRequired())
	}
	team, err := teamTx(ctx, tx, festID, gameID, number, venues)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
insert into od_contested(game_id, question, fest_team_id, answer, created_by, created_at)
values(?, ?, ?, ?, ?, ?)
on conflict(game_id, question, fest_team_id) do update set answer = excluded.answer`,
		gameID, question, team.ID, answer, util.NullableInt64(userID), util.UtcNow())
	return err
}

// DeleteTx takes the team's contested answer on the question back.
func DeleteTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, venues []int64, question int, number int64) error {
	team, err := teamTx(ctx, tx, festID, gameID, number, venues)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `delete from od_contested where game_id = ? and question = ? and fest_team_id = ?`,
		gameID, question, team.ID)
	return err
}

// RuleTx sets the jury's ruling on an answer to a question: accepted,
// rejected, or pending again.
func RuleTx(ctx context.Context, tx *sql.Tx, gameID, userID int64, question int, answer, status string) error {
	key := Key(answer)
	if key == "" || question < 0 {
		return corei18n.User(dopestrings.Default.Od.Contested.AnswerRequired())
	}
	switch status {
	case games.ContestedPending:
		_, err := tx.ExecContext(ctx, `delete from od_contested_rulings where game_id = ? and question = ? and answer_key = ?`,
			gameID, question, key)
		return err
	case games.ContestedAccepted, games.ContestedRejected:
		_, err := tx.ExecContext(ctx, `
insert into od_contested_rulings(game_id, question, answer_key, accepted, ruled_by, ruled_at)
values(?, ?, ?, ?, ?, ?)
on conflict(game_id, question, answer_key) do update set accepted = excluded.accepted, ruled_by = excluded.ruled_by, ruled_at = excluded.ruled_at`,
			gameID, question, key, util.BoolToInt(status == games.ContestedAccepted), util.NullableInt64(userID), util.UtcNow())
		return err
	}
	return corei18n.User(dopestrings.Default.Od.Contested.StatusInvalid())
}

// RefreshTx writes the Game's contested answers into its document, without their text,
// and returns the document when that changed it: nil for a Game that takes
// none, or whose document already says the same. A fest that takes no
// contested answers gets none in its documents, so its standings drop the accepted ones.
func RefreshTx(ctx context.Context, tx *sql.Tx, festID, gameID int64) ([]byte, error) {
	doc, err := store.LoadGameDoc(ctx, tx, festID, gameID)
	if err != nil {
		return nil, err
	}
	if _, ok := games.As[games.Contestable](doc.GameType); !ok {
		return nil, nil
	}
	var summary []games.ODContested
	on, err := Enabled(ctx, tx, festID)
	if err != nil {
		return nil, err
	}
	if on {
		list, err := List(ctx, tx, festID, gameID)
		if err != nil {
			return nil, err
		}
		for _, a := range list {
			summary = append(summary, games.ODContested{Question: a.Question, Number: a.Number, Status: a.Status})
		}
	}
	next, err := Splice([]byte(doc.State), summary)
	if err != nil || bytes.Equal(next, []byte(doc.State)) {
		return nil, err
	}
	return next, flatgame.SetStateTx(ctx, tx, festID, gameID, string(next))
}

// Splice puts the contested answers into a document, or takes the key away when there
// are none.
func Splice(state []byte, summary []games.ODContested) ([]byte, error) {
	obj, err := games.RawJSONObject(string(state))
	if err != nil {
		return nil, err
	}
	_, had := obj[games.ContestedKey]
	if len(summary) == 0 {
		if !had {
			return state, nil
		}
		delete(obj, games.ContestedKey)
	} else if obj[games.ContestedKey], err = json.Marshal(summary); err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}

// CarryOver keeps the contested answers a document held in one that replaces it whole:
// a page that sends the document back sends whatever list it last saw.
func CarryOver(next, stored []byte) []byte {
	var old struct {
		Contested []games.ODContested `json:"contested"`
	}
	if err := json.Unmarshal(stored, &old); err != nil {
		return next
	}
	if out, err := Splice(next, old.Contested); err == nil {
		return out
	}
	return next
}
