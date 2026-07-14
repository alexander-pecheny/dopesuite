package typoedit

import "testing"

// The pass inserts characters you cannot see, so every expectation below spells
// them out:   is the non-breaking space, ‑ the non-breaking hyphen.
func TestPass(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			"quotes, dashes, and the gluing that comes with them",
			`? В романе "Мастер и Маргарита" ОН - кот.`,
			"? В романе «Мастер и Маргарита» ОН — кот.",
		},
		{
			// The whole reason this package exists: a leading "-" is a list item, not
			// a dash flanked by whitespace, and a naive pass would eat the list.
			"a list item's dash survives",
			"^ Сборник:\n- Первый источник\n- Второй источник",
			"^ Сборник:\n- Первый источник\n- Второй источник",
		},
		{
			"short hyphenated words get a non-breaking hyphen",
			"? Он что-то знал о нём.",
			"? Он что‑то знал о нём.",
		},
		{
			"markers and URLs are left alone",
			"! Ответ\n= Зачёт\n^ https://example.com/a-b\n@ Иван Иванов",
			"! Ответ\n= Зачёт\n^ https://example.com/a-b\n@ Иван Иванов",
		},
		{
			// A pasted Wikipedia link, which is what chgk sources are made of.
			"percent-escapes decode",
			"^ https://ru.wikipedia.org/wiki/%D0%91%D0%B5%D0%B3%D0%B5%D0%BC%D0%BE%D1%82",
			"^ https://ru.wikipedia.org/wiki/Бегемот",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Pass(c.in); got != c.want {
				t.Errorf("Pass(%q)\n got: %q\nwant: %q", c.in, got, c.want)
			}
		})
	}
}

// TestPassIsIdempotent: the button is a button — a user will press it twice.
func TestPassIsIdempotent(t *testing.T) {
	src := "? В романе \"Мастер и Маргарита\" ОН - кот, что-то знавший о нём.\n! Бегемот"
	once := Pass(src)
	if twice := Pass(once); twice != once {
		t.Errorf("second pass changed the text:\n once: %q\ntwice: %q", once, twice)
	}
}
