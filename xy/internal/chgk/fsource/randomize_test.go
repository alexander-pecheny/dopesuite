package fsource

import (
	"math/rand"
	"testing"
)

// TestRandomizeRenumbers checks the two things --randomize promises: the
// questions all survive, and they are numbered 1..N in their new order.
func TestRandomizeRenumbers(t *testing.T) {
	src := "### Пакет\n\n"
	for _, q := range []string{"Первый", "Второй", "Третий", "Четвёртый"} {
		src += "? " + q + "?\n! да\n\n"
	}
	doc := Parse(src, "chgk")
	Randomize(doc, rand.New(rand.NewSource(7)))

	var texts []string
	n := 0
	for _, el := range doc {
		q, ok := el.Content.(*Question)
		if !ok || el.Type != "Question" {
			continue
		}
		n++
		if got := q.Get("number"); got != n {
			t.Errorf("question %d numbered %v", n, got)
		}
		texts = append(texts, toStr(q.Get("question")))
	}
	if n != 4 {
		t.Fatalf("kept %d questions, want 4", n)
	}
	seen := map[string]bool{}
	for _, s := range texts {
		seen[s] = true
	}
	if len(seen) != 4 {
		t.Errorf("questions not distinct after shuffle: %v", texts)
	}
}
