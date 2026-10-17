package qyserver

import (
	"context"

	"qy/qy/domain/contest"
	"qy/qy/domain/tally"
	"qy/qy/storage/store"
)

// The shortlist is computed from the likes whenever it is asked for, until an
// admin freezes it; from then on the shortlist table is what it is.

// likeCounts is each question's likes from the expert and the people's jury.
func likeCounts(ctx context.Context, q store.Querier, year int) (map[int64][2]int, error) {
	rows, err := q.QueryContext(ctx, `
select l.question_id, j.kind, count(*) from likes l
join users u on u.id = l.user_id
join jurors j on j.year = ? and j.telegram_username = u.telegram_username
join questions qq on qq.id = l.question_id and qq.year = ?
group by l.question_id, j.kind`, year, year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][2]int{}
	for rows.Next() {
		var id int64
		var kind string
		var n int
		if err := rows.Scan(&id, &kind, &n); err != nil {
			return nil, err
		}
		c := out[id]
		if kind == store.Expert {
			c[0] = n
		} else {
			c[1] = n
		}
		out[id] = c
	}
	return out, rows.Err()
}

// candidate is a numbered, approved question with its likes.
type candidate struct {
	store.Question
	expert, people int
}

func candidates(ctx context.Context, q store.Querier, c contest.Contest) ([]candidate, error) {
	qs, err := store.Questions(ctx, q, c.Year, "qy is not null and status = 'approved'")
	if err != nil {
		return nil, err
	}
	counts, err := likeCounts(ctx, q, c.Year)
	if err != nil {
		return nil, err
	}
	out := make([]candidate, len(qs))
	for i, qq := range qs {
		n := counts[qq.ID]
		out[i] = candidate{qq, n[0], n[1]}
	}
	return out, nil
}

func computeShortlist(c contest.Contest, cands []candidate) []tally.Pick {
	in := make([]tally.Candidate, len(cands))
	for i, cd := range cands {
		in[i] = tally.Candidate{
			ID: cd.ID, QY: cd.QY, Expert: cd.expert, People: cd.people,
			Excluded: cd.AuthorDeclined || cd.Pin == "remove",
			Pinned:   cd.Pin == "add",
		}
	}
	return tally.Shortlist(in, c.ExpertCutoff, c.PeopleCutoff)
}

// shortlistIDs is the shortlist as it stands: the frozen one when there is
// one, the computed one before.
func shortlistIDs(ctx context.Context, q store.Querier, c contest.Contest) ([]int64, error) {
	if c.ShortlistFrozenAt != "" {
		return store.Collect(ctx, q, store.Scalar[int64], `select question_id from shortlist where year = ?`, c.Year)
	}
	cands, err := candidates(ctx, q, c)
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, p := range computeShortlist(c, cands) {
		out = append(out, p.ID)
	}
	return out, nil
}

// shortlistQuestions loads the shortlist's questions in QY order.
func shortlistQuestions(ctx context.Context, q store.Querier, c contest.Contest) ([]store.Question, error) {
	ids, err := shortlistIDs(ctx, q, c)
	if err != nil {
		return nil, err
	}
	in := map[int64]bool{}
	for _, id := range ids {
		in[id] = true
	}
	all, err := store.Questions(ctx, q, c.Year, "qy is not null")
	if err != nil {
		return nil, err
	}
	var out []store.Question
	for _, qq := range all {
		if in[qq.ID] {
			out = append(out, qq)
		}
	}
	return out, nil
}
