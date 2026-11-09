package gq

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	if n != (Counts{Packs: 1, Questions: 2, People: 2, Authors: 2, Editors: 2}) {
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
	}, Editors: []Editor{{Name: "Пётр Петров", RatingID: 707}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// A tour with no editors of its own has the pack's.
func TestATourWithoutEditorsHasThePacks(t *testing.T) {
	got, err := importFixture(t).Question(context.Background(), 13)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Editor{{Name: "Иван Иванов"}}; !reflect.DeepEqual(got.Editors, want) {
		t.Errorf("editors = %+v, want %+v", got.Editors, want)
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
