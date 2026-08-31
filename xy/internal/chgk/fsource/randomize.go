package fsource

import "math/rand"

// Randomize is `--randomize`: shuffle the document and renumber the questions
// 1..N. chgksuite shuffles the WHOLE structure, headings and meta included, so
// they end up scattered among the questions — a quirk, but the flag exists to
// re-order a package for testing, where nothing else is in it.
func Randomize(d Doc, r *rand.Rand) {
	r.Shuffle(len(d), func(i, j int) { d[i], d[j] = d[j], d[i] })
	n := 1
	for _, el := range d {
		if q, ok := el.Content.(*Question); ok && el.Type == "Question" {
			q.Set("number", n)
			n++
		}
	}
}
