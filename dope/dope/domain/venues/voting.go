package venues

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"dope/dope/platform/util"
	"dope/dope/storage/store"
)

// Голосование (CONTEXT.md): an optional poll on a Слот, reached by its own
// unguessable link, whose candidates are the tournaments buff knows to be
// playable that day. The result is advice only.

// Ballot kinds.
const (
	KindOne    = "one"
	KindAny    = "any"
	KindRanked = "ranked"
)

// RankedDepth is how many tournaments a ranked ballot orders, and BordaPoints
// what each place pays.
const RankedDepth = 3

var bordaPoints = []int{3, 2, 1}

// Candidate is one tournament on the ballot.
type Candidate struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Voting is a Слот's poll.
type Voting struct {
	ID         int64
	SlotID     int64
	Token      string
	Kind       string
	PerTeam    bool
	OpensAt    string
	ClosesAt   string
	Candidates []Candidate
	Frozen     bool
}

// Ballot is one voter's answer. Choice holds candidate ids: one for KindOne,
// any number for KindAny, up to RankedDepth in order for KindRanked.
type Ballot struct {
	ID        int64
	VotingID  int64
	UserID    int64
	Voter     string
	TeamName  string
	Choice    []int64
	Discarded bool
	CreatedAt string
}

// TallyRow is one candidate's standing in the count.
type TallyRow struct {
	Candidate Candidate
	Score     int
}

// Open reports whether the poll takes ballots at now.
func (v Voting) Open(now time.Time) bool {
	if opens, ok := ParseTime(v.OpensAt); ok && now.Before(opens) {
		return false
	}
	return !v.Closed(now)
}

// Closed reports whether the poll is over — after which the tally shows to
// anyone holding the link.
func (v Voting) Closed(now time.Time) bool {
	closes, ok := ParseTime(v.ClosesAt)
	return ok && !now.Before(closes)
}

// Tally counts the ballots the way the poll's kind says. In per-team mode the
// ballots are grouped by trimmed, case-folded team name and each team counts
// once — its latest ballot. Discarded ballots are excluded.
func Tally(v Voting, ballots []Ballot) []TallyRow {
	counted := countedBallots(v.PerTeam, ballots)
	score := map[int64]int{}
	for _, b := range counted {
		switch v.Kind {
		case KindRanked:
			for place, id := range b.Choice {
				if place >= len(bordaPoints) {
					break
				}
				score[id] += bordaPoints[place]
			}
		default:
			for _, id := range b.Choice {
				score[id]++
			}
		}
	}
	rows := make([]TallyRow, 0, len(v.Candidates))
	for _, c := range v.Candidates {
		rows = append(rows, TallyRow{Candidate: c, Score: score[c.ID]})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Score > rows[j].Score })
	return rows
}

// countedBallots drops the discarded ones and, in per-team mode, keeps one
// ballot per team — the last one filed.
func countedBallots(perTeam bool, ballots []Ballot) []Ballot {
	kept := make([]Ballot, 0, len(ballots))
	for _, b := range ballots {
		if !b.Discarded {
			kept = append(kept, b)
		}
	}
	if !perTeam {
		return kept
	}
	byTeam := map[string]Ballot{}
	order := []string{}
	for _, b := range kept {
		key := strings.ToLower(strings.Join(strings.Fields(b.TeamName), " "))
		if _, seen := byTeam[key]; !seen {
			order = append(order, key)
		}
		byTeam[key] = b
	}
	out := make([]Ballot, 0, len(order))
	for _, key := range order {
		out = append(out, byTeam[key])
	}
	return out
}

// NormalizeChoice keeps only ids the poll offers, in the form its kind takes:
// one id, any number of them, or at most RankedDepth in order and each once.
func NormalizeChoice(v Voting, ids []int64) []int64 {
	offered := map[int64]bool{}
	for _, c := range v.Candidates {
		offered[c.ID] = true
	}
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !offered[id] || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if v.Kind == KindOne {
			break
		}
		if v.Kind == KindRanked && len(out) == RankedDepth {
			break
		}
	}
	return out
}

// ---- persistence -------------------------------------------------------------

const votingSelect = `
select id, slot_id, token, kind, per_team, coalesce(opens_at, ''), coalesce(closes_at, ''), candidates_json, frozen
from slot_votings where `

func scanVoting(row interface{ Scan(...any) error }) (Voting, error) {
	var v Voting
	var perTeam, frozen int
	var candidates string
	err := row.Scan(&v.ID, &v.SlotID, &v.Token, &v.Kind, &perTeam, &v.OpensAt, &v.ClosesAt, &candidates, &frozen)
	v.PerTeam, v.Frozen = perTeam == 1, frozen == 1
	if candidates != "" {
		_ = json.Unmarshal([]byte(candidates), &v.Candidates)
	}
	return v, err
}

// SlotVoting is a Слот's poll; sql.ErrNoRows when it has none.
func SlotVoting(ctx context.Context, q store.Queryer, slotID int64) (Voting, error) {
	return scanVoting(q.QueryRowContext(ctx, votingSelect+`slot_id = ?`, slotID))
}

// VotingByToken is the poll a voting link names.
func VotingByToken(ctx context.Context, q store.Queryer, token string) (Voting, error) {
	if strings.TrimSpace(token) == "" {
		return Voting{}, sql.ErrNoRows
	}
	return scanVoting(q.QueryRowContext(ctx, votingSelect+`token = ?`, token))
}

// ErrNoCandidates refuses a poll with nothing to vote for.
var ErrNoCandidates = errors.New("выберите хотя бы один турнир")

// SaveVotingTx creates or rewrites a Слот's poll. A poll that has taken a
// ballot is frozen: its candidates stay as the voters saw them.
func SaveVotingTx(ctx context.Context, tx *sql.Tx, slotID int64, kind string, perTeam bool, opensAt, closesAt string, candidates []Candidate) error {
	if len(candidates) == 0 {
		return ErrNoCandidates
	}
	switch kind {
	case KindOne, KindAny, KindRanked:
	default:
		return errors.New("неизвестный вид голосования")
	}
	encoded, err := json.Marshal(candidates)
	if err != nil {
		return err
	}
	existing, err := SlotVoting(ctx, tx, slotID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = store.InsertReturningID(ctx, tx, `
insert into slot_votings(slot_id, token, kind, per_team, opens_at, closes_at, candidates_json, frozen, created_at)
values(?, ?, ?, ?, ?, ?, ?, 0, ?)`, slotID, NewToken(), kind, util.BoolToInt(perTeam),
			util.NullableString(FormatTime(opensAt)), util.NullableString(FormatTime(closesAt)), string(encoded), util.UtcNow())
		return err
	}
	if err != nil {
		return err
	}
	if existing.Frozen {
		encoded, err = json.Marshal(existing.Candidates)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `
update slot_votings set kind = ?, per_team = ?, opens_at = ?, closes_at = ?, candidates_json = ?
where id = ?`, kind, util.BoolToInt(perTeam),
		util.NullableString(FormatTime(opensAt)), util.NullableString(FormatTime(closesAt)), string(encoded), existing.ID)
	return err
}

// VotingBallots are a poll's ballots in filing order.
func VotingBallots(ctx context.Context, q store.Queryer, votingID int64) ([]Ballot, error) {
	return store.CollectRows(ctx, q, `
select b.id, b.voting_id, b.user_id,
       coalesce(nullif(u.telegram_username, ''), nullif(u.username, ''), ''),
       coalesce(b.team_name, ''), b.choice_json, b.discarded, b.created_at
from slot_ballots b join users u on u.id = b.user_id
where b.voting_id = ?
order by b.created_at, b.id`, []any{votingID}, func(rows *sql.Rows) (Ballot, error) {
		var b Ballot
		var choice string
		var discarded int
		err := rows.Scan(&b.ID, &b.VotingID, &b.UserID, &b.Voter, &b.TeamName, &choice, &discarded, &b.CreatedAt)
		b.Discarded = discarded == 1
		if choice != "" {
			_ = json.Unmarshal([]byte(choice), &b.Choice)
		}
		return b, err
	})
}

// UserBallot is a voter's own ballot; sql.ErrNoRows when they have not voted.
func UserBallot(ctx context.Context, q store.Queryer, votingID, userID int64) (Ballot, error) {
	ballots, err := VotingBallots(ctx, q, votingID)
	if err != nil {
		return Ballot{}, err
	}
	for _, b := range ballots {
		if b.UserID == userID {
			return b, nil
		}
	}
	return Ballot{}, sql.ErrNoRows
}

// CastBallotTx files or replaces a voter's ballot and freezes the candidates.
func CastBallotTx(ctx context.Context, tx *sql.Tx, v Voting, userID int64, teamName string, choice []int64) error {
	encoded, err := json.Marshal(NormalizeChoice(v, choice))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
insert into slot_ballots(voting_id, user_id, team_name, choice_json, discarded, created_at)
values(?, ?, ?, ?, 0, ?)
on conflict(voting_id, user_id) do update set team_name = excluded.team_name, choice_json = excluded.choice_json`,
		v.ID, userID, strings.TrimSpace(teamName), string(encoded), util.UtcNow()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update slot_votings set frozen = 1 where id = ?`, v.ID)
	return err
}

// DiscardBallotTx is the Representative's «Отклонить»; discarded reverses it.
func DiscardBallotTx(ctx context.Context, tx *sql.Tx, votingID, ballotID int64, discarded bool) error {
	_, err := tx.ExecContext(ctx, `update slot_ballots set discarded = ? where id = ? and voting_id = ?`,
		util.BoolToInt(discarded), ballotID, votingID)
	return err
}
