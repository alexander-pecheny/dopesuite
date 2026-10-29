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
	err := d.db.QueryRowContext(ctx, `
select q.handout, q.handout_pic, q.text, q.answer, q.zachet, q.nezachet, q.comment, q.source,
       p.title, p.tournament_id,
       coalesce((select group_concat(coalesce(nullif(a.alias, ''), pe.name), ', ')
                 from question_authors a join people pe on pe.id = a.person_id
                 where a.question_id = q.id), '')
from questions q join packs p on p.id = q.pack_id where q.id = ?`, id).Scan(
		&handout, &pic, &q.Text, &q.Answer, &q.Zachet, &q.Nezachet, &q.Comment, &q.Source,
		&f.Tournament, &f.TournamentID, &q.Author)
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
	return f, nil
}
