package gq

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"qy/qy/domain/question"
)

func importFixture(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gq.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	dump, err := os.Open("testdata/dump.sql")
	if err != nil {
		t.Fatal(err)
	}
	defer dump.Close()
	n, err := Import(context.Background(), db, dump)
	if err != nil {
		t.Fatal(err)
	}
	if n != (Counts{Packs: 1, Questions: 1, People: 2, Authors: 2}) {
		t.Fatalf("counts = %+v", n)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestImportedQuestion(t *testing.T) {
	d := importFixture(t)
	got, err := d.Question(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	want := Found{TournamentID: 8380, Tournament: "Кубок", Question: question.Question{
		Text:   question.WithHandout("Вопрос\\про\nИКС.", "Текст\nhttps://gotquestions.online/pics/1/10.png"),
		Answer: "икс", Author: "Пётр Петров, Ваня",
	}}
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestHiddenQuestionsAreNotImported(t *testing.T) {
	d := importFixture(t)
	for _, id := range []int64{11, 12, 99} {
		if _, err := d.Question(context.Background(), id); !errors.Is(err, ErrUnavailable) {
			t.Errorf("question %d: err = %v, want ErrUnavailable", id, err)
		}
	}
}
