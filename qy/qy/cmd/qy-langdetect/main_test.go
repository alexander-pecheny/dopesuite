package main

import (
	"strings"
	"testing"

	"qy/qy/domain/lang"
)

// The short questions whatlanggo misread, and the hard cases of the labelled
// set: old texts typed with a Latin i, a Ukrainian question with a Russian
// note for the host.
func TestDetect(t *testing.T) {
	d := newDetector()
	for _, c := range []struct{ text, want string }{
		{"Кого зовут Крат в мультфильме Доктор Кроко?\nбегемота", lang.Russian},
		{"Назовите город, в котором родился ЭТОТ писатель.\nКиев", lang.Russian},
		{"Тарас Шевченко каже, що ВІН завжди поруч із нами, коли ми згадуємо минуле. Назвіть ЙОГО двома словами.\nпалац пам’яті", lang.Ukrainian},
		{"Яку тварину зобразили на гербі цього міста у XVII столітті?\nлева", lang.Ukrainian},
		{"Вовк каже: \"Йшов я лiсом до старої карги [...]\".\nI здибав я дiвчину, що несла пироги", lang.Ukrainian},
		{"[Ведущему: вопрос на украинском.] Алекс Мален неодноразово брав участь у різних телешоу. Назвіть ІКС словами, що починаються однаково.\nпалац пам’яті.", lang.Ukrainian},
		{"Што, паводле легенды, убачыў князь на беразе ракі?\nкрыж", lang.Belarusian},
		{"", lang.Russian},
	} {
		if got := detect(d, c.text); got != c.want {
			t.Errorf("detect(%.40q) = %s, want %s", c.text, got, c.want)
		}
	}
}

func TestRunAnswersEachLine(t *testing.T) {
	var out strings.Builder
	in := `{"id": 1, "text": "Што, паводле легенды, убачыў князь на беразе ракі?"}` + "\n" + `{"id": 2, "text": ""}` + "\n"
	if err := run(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if want := `{"id":1,"lang":"be"}` + "\n" + `{"id":2,"lang":"ru"}` + "\n"; out.String() != want {
		t.Errorf("got %q", out.String())
	}
}
