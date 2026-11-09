package gq

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	_ "modernc.org/sqlite"

	"qy/qy/domain/question"
)

// site prefixes the picture paths gotquestions stores.
const site = "https://gotquestions.online"

// DB answers from a copy of gotquestions made by Import.
type DB struct{ db *sql.DB }

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return &DB{db}, nil
}

func (d *DB) Question(ctx context.Context, id int64) (Found, error) {
	var f Found
	var q question.Question
	var handout, pic string
	var tour sql.NullInt64
	var pack int64
	err := d.db.QueryRowContext(ctx, `
select q.handout, q.handout_pic, q.text, q.answer, q.zachet, q.nezachet, q.comment, q.source,
       p.title, p.tournament_id, q.tour_id, q.pack_id,
       coalesce((select group_concat(coalesce(nullif(a.alias, ''), pe.name), ', ')
                 from question_authors a join people pe on pe.id = a.person_id
                 where a.question_id = q.id), '')
from questions q join packs p on p.id = q.pack_id where q.id = ?`, id).Scan(
		&handout, &pic, &q.Text, &q.Answer, &q.Zachet, &q.Nezachet, &q.Comment, &q.Source,
		&f.Tournament, &f.TournamentID, &tour, &pack, &q.Author)
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrUnavailable
	}
	if err != nil {
		return f, err
	}
	if strings.HasPrefix(pic, "/") {
		pic = site + pic
	}
	if pic != "" {
		handout = strings.TrimSpace(handout + "\n" + pic)
	}
	q.Text = question.WithHandout(q.Text, handout)
	f.Tournament = strings.TrimSpace(f.Tournament)
	f.Question = q
	f.Editors, err = d.editors(ctx, tour, pack)
	return f, err
}

// editors are the tour's editors, or the pack's when the tour has none listed.
func (d *DB) editors(ctx context.Context, tour sql.NullInt64, pack int64) ([]Editor, error) {
	const cols = `coalesce(nullif(e.alias, ''), pe.name), coalesce(pe.player_id, 0)`
	if tour.Valid {
		out, err := d.editorRows(ctx, `select `+cols+` from tour_editors e join people pe on pe.id = e.person_id where e.tour_id = ?`, tour.Int64)
		if err != nil || len(out) > 0 {
			return out, err
		}
	}
	return d.editorRows(ctx, `select `+cols+` from pack_editors e join people pe on pe.id = e.person_id where e.pack_id = ?`, pack)
}

func (d *DB) editorRows(ctx context.Context, query string, id int64) ([]Editor, error) {
	rows, err := d.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Editor
	for rows.Next() {
		var e Editor
		if err := rows.Scan(&e.Name, &e.RatingID); err != nil {
			return nil, err
		}
		e.Name = strings.TrimSpace(e.Name)
		out = append(out, e)
	}
	return out, rows.Err()
}
