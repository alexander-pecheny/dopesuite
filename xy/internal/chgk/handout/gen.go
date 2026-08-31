package handout

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/i18n"
	"xy/internal/chgk/inline"
)

// The 4s2hndt half of handouts (chgksuite/handouter/gen.py): a package's
// questions scanned for their «[Раздаточный материал: …]» brackets, and one
// .hndt block written per handout found.

// GenerateOptions are the switches `handouts generate` takes.
type GenerateOptions struct {
	// Language picks the regex set the handout bracket is recognised by.
	Language string
	// Separate writes a file per question instead of one for the package.
	Separate bool
	// ListHandouts also writes the human-readable «which questions have one».
	ListHandouts bool
}

// File is one file the generator wants written, named relative to the package.
type File struct {
	Name    string
	Content string
}

// Warning is a question the generator could not read cleanly: chgksuite prints
// these and carries on, and so does the caller.
type Warning struct {
	Number string
	Text   string
}

// Generate is generate_handouts. dir is where the package lives, which is where
// a picture it names is looked for.
func Generate(doc fsource.Doc, base, dir string, o GenerateOptions) ([]File, []Warning, error) {
	rx, err := i18n.LoadRegexes(o.Language)
	if err != nil {
		return nil, nil, err
	}
	short := rx.Get("handout_short")
	if short == nil {
		return nil, nil, fmt.Errorf("язык %q: нет выражения handout_short", o.Language)
	}
	handoutRe, err := regexp.Compile(`(?s)\[` + short.String() + `.+?:( |\n)(?P<handout_text>.+?)\]`)
	if err != nil {
		return nil, nil, err
	}

	var handouts []handout
	var warnings []Warning
	for _, p := range doc {
		q, ok := p.Content.(*fsource.Question)
		if p.Type != "Question" || !ok {
			continue
		}
		number := fmt.Sprintf("%v", q.Get("number"))
		text := questionText(q.Get("question"))
		if m := handoutRe.FindStringSubmatch(text); m != nil {
			body := postprocess(m[handoutRe.SubexpIndex("handout_text")])
			h := handout{number: number, text: body}
			if name, ok := imageIn(body); ok {
				// chgksuite writes the path its own image search resolved, which
				// is an absolute one; the renderer on either side reads it back.
				path, err := filepath.Abs(filepath.Join(dir, name))
				if err != nil {
					return nil, nil, err
				}
				if _, err := os.Stat(path); err != nil {
					warnings = append(warnings, Warning{number,
						"файл картинки не найден, добавьте раздатку вручную"})
					continue
				}
				h.text, h.image = "", path
			}
			handouts = append(handouts, h)
			continue
		}
		lower := strings.ToLower(text)
		if strings.Contains(lower, "раздат") || strings.Contains(lower, "роздан") || strings.Contains(lower, "(img") {
			warnings = append(warnings, Warning{number, "раздатка, похоже, размечена неправильно"})
			handouts = append(handouts, handout{number: number, text: postprocess(text)})
		}
	}

	byQuestion := map[string][]string{}
	var blocks []string
	var order []string
	for _, h := range handouts {
		value, prefix := h.text, ""
		if h.image != "" {
			value, prefix = h.image, "image: "
		}
		head := ""
		if !o.Separate {
			head = "for_question: " + h.number + "\n"
		}
		block := head + "columns: 3\n\n" + prefix + value
		blocks = append(blocks, block)
		if _, seen := byQuestion[h.number]; !seen {
			order = append(order, h.number)
		}
		byQuestion[h.number] = append(byQuestion[h.number], block)
	}

	var files []File
	if o.Separate {
		for _, number := range order {
			v := byQuestion[number]
			if len(v) == 1 {
				files = append(files, File{fmt.Sprintf("%s_q%s.hndt", base, pad2(number)), v[0]})
				continue
			}
			for i, block := range v {
				files = append(files, File{fmt.Sprintf("%s_q%s_%d.hndt", base, pad2(number), i+1), block})
			}
		}
	} else {
		files = append(files, File{base + ".hndt", strings.Join(blocks, "\n---\n")})
	}
	if o.ListHandouts {
		files = append(files, File{base + "_handouts_list.txt", handoutsList(handouts, doc)})
	}
	return files, warnings, nil
}

type handout struct{ number, text, image string }

func postprocess(s string) string { return strings.ReplaceAll(s, `\_`, "_") }

// imageIn reports the picture an (img …) directive in the handout names.
func imageIn(text string) (string, bool) {
	for _, r := range inline.Parse4sElem(text) {
		if r.Kind != "img" {
			continue
		}
		if im, ok := inline.ParseImg(r.Text); ok {
			return im.Name, true
		}
	}
	return "", false
}

// questionText flattens a question that came out as a list, as gen.py does with
// itertools.chain.
func questionText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var lines []string
		for _, item := range t {
			lines = append(lines, strings.Split(questionText(item), "\n")...)
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func pad2(number string) string {
	if len(number) < 2 {
		return "0" + number
	}
	return number
}

// handoutsList is generate_handouts_list: the numbers with a handout, straight
// through and then by tour.
func handoutsList(handouts []handout, doc fsource.Doc) string {
	has := map[int]bool{}
	var numbers []int
	for _, h := range handouts {
		n, err := strconv.Atoi(h.number)
		if err != nil || has[n] {
			continue
		}
		has[n] = true
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)

	var b strings.Builder
	b.WriteString("ВОПРОСЫ С РАЗДАТОЧНЫМ МАТЕРИАЛОМ:\n\n")
	b.WriteString("Сквозная нумерация:\n" + joinInts(numbers) + "\n\n")
	b.WriteString("По турам:\n")

	tour := 0
	byTour := map[int][]string{}
	var tours []int
	for _, p := range doc {
		if p.Type == "section" {
			tour++
			tours = append(tours, tour)
			byTour[tour] = nil
			continue
		}
		q, ok := p.Content.(*fsource.Question)
		if p.Type != "Question" || !ok {
			continue
		}
		if tour == 0 {
			tour = 1
			tours = append(tours, tour)
			byTour[tour] = nil
		}
		number := fmt.Sprintf("%v", q.Get("number"))
		if n, err := strconv.Atoi(number); err == nil && has[n] {
			byTour[tour] = append(byTour[tour], number)
		}
	}
	for _, t := range tours {
		if len(byTour[t]) == 0 {
			fmt.Fprintf(&b, "Тур %d: нет раздаток\n", t)
			continue
		}
		fmt.Fprintf(&b, "Тур %d: %s\n", t, strings.Join(byTour[t], ", "))
	}
	return b.String()
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ", ")
}
