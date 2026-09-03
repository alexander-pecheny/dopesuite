package textparse

import (
	"regexp"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/typo"
)

// ParseDB is parser_db.chgk_parse_db: db.chgk.info's own text export, the format
// a file opening with the Championship header is in. chgksuite reads it with a PLY lexer of
// fifteen exclusive states; this is that lexer, rule for rule and in the order
// PLY tries them — definition order, first match at the position wins.
//
// fetch downloads a picture or sound the export names, as chgksuite does before
// lexing. A nil fetch skips it; the text comes out the same either way, since a
// failed download is swallowed there too.
func ParseDB(text string, fetch Fetcher) fsource.Doc {
	l := &dbLexer{opts: typo.DefaultOptions()}
	l.run(replaceDBHandouts(text, fetch))
	return l.doc
}

// Fetcher saves what is at url under name, in whatever directory it was built
// for. It is called once per reference.
type Fetcher func(url, name string) error

// IsDBExport reports whether a .txt is db.chgk.info's export rather than a
// package written by hand; chgksuite decides on the first ten characters.
func IsDBExport(text string) bool {
	r := []rune(text)
	return len(r) >= 10 && string(r[:10]) == "Чемпионат:"
}

// dbPicBaseURL and dbAudBaseURL are where db.chgk.info keeps what its exports
// refer to by bare filename.
const (
	dbPicBaseURL = "http://db.chgk.info/images/db/"
	dbAudBaseURL = "http://db.chgk.info/sounds/db/"
)

var (
	reDBHandoutRef = regexp.MustCompile(`(?i)\((pic|aud):` + ws + `([\d\.\w]+)\)`)
	reDBListItem   = regexp.MustCompile(`^` + ws + `{3}(\d+)\.` + ws + `(.+)$`)
	reDBBlankLine  = regexp.MustCompile(`^\n\n`)
	reDBLine       = regexp.MustCompile(`^.+`)
	reDBNewline    = regexp.MustCompile(`^\n`)
)

// replaceDBHandouts is parser_db.replace_handouts: "(pic: x.jpg)" becomes
// "(img x.jpg)" and the file is fetched unless it is already on disk.
func replaceDBHandouts(text string, fetch Fetcher) string {
	return reDBHandoutRef.ReplaceAllStringFunc(text, func(ref string) string {
		m := reDBHandoutRef.FindStringSubmatch(ref)
		kind, base := "aud", dbAudBaseURL
		if strings.EqualFold(m[1], "pic") {
			kind, base = "img", dbPicBaseURL
		}
		if fetch != nil {
			_ = fetch(base+m[2], m[2])
		}
		return "(" + kind + " " + m[2] + ")"
	})
}

type dbState int

const (
	dbInitial dbState = iota
	dbTitle
	dbURL
	dbDate
	dbEditor
	dbInfo
	dbTour
	dbQuestion
	dbHandout
	dbAnswer
	dbZachet
	dbNezachet
	dbComment
	dbSource
	dbAuthor
)

// dbFields is the dict parser_db builds a question in. question, answer,
// comment and source start as lists and become plain strings unless the export
// numbered them, which is how a blitz and a multi-source arrive.
type dbFields struct {
	number                            int
	question, answer, comment, source any
	zachet, nezachet, author          string
}

type dbLexer struct {
	state dbState
	text  string
	doc   fsource.Doc
	num   int
	q     *dbFields
	opts  typo.Options
}

type dbRule struct {
	re *regexp.Regexp
	do func(l *dbLexer, match string)
}

func (l *dbLexer) run(text string) {
	for pos := 0; pos < len(text); {
		rest := text[pos:]
		matched := false
		for _, r := range dbRules[l.state] {
			loc := r.re.FindStringIndex(rest)
			if loc == nil {
				continue
			}
			r.do(l, rest[:loc[1]])
			pos += loc[1]
			matched = true
			break
		}
		if !matched {
			// PLY's t_ANY_error: skip the character and read on.
			pos += len(string([]rune(rest)[0]))
		}
	}
	l.appendQuestion()
}

// enter opens a field, so the accumulator starts empty; goTo only changes state,
// which is what the "\n\n" rules that close a field do.
func (l *dbLexer) enter(s dbState) { l.state, l.text = s, "" }
func (l *dbLexer) goTo(s dbState)  { l.state = s }

func (l *dbLexer) push(typ string, v any) {
	l.doc = append(l.doc, fsource.Pair{Type: typ, Content: v})
}

// cur is the question being read. An export that names a field before opening a
// question crashes chgksuite; here it opens an unnumbered one, which is dropped.
func (l *dbLexer) cur() *dbFields {
	if l.q == nil {
		l.q = &dbFields{question: []any{}, answer: []any{}, comment: []any{}, source: []any{}}
	}
	return l.q
}

// initQuestion is parser_db.init_question: file the question in hand and open
// the next one. The tour header opens one too, so the first question of a tour
// is numbered 2 — invisible under chgksuite's default numbers_handling, which is
// why it has never mattered.
func (l *dbLexer) initQuestion() {
	l.appendQuestion()
	l.num++
	l.q = nil
	l.cur().number = l.num
}

// appendQuestion files the question in hand, dropping the fields nothing was
// written to (Python's "remove empty values").
func (l *dbLexer) appendQuestion() {
	if l.q == nil {
		return
	}
	q := fsource.NewQuestion()
	for _, f := range []struct {
		name string
		v    any
	}{
		{"number", l.q.number}, {"question", l.q.question}, {"answer", l.q.answer},
		{"comment", l.q.comment}, {"source", l.q.source},
		{"zachet", l.q.zachet}, {"nezachet", l.q.nezachet}, {"author", l.q.author},
	} {
		if dbTruthy(f.v) {
			q.Set(f.name, f.v)
		}
	}
	if !q.Empty() {
		l.push("Question", q)
	}
}

func dbTruthy(v any) bool {
	switch t := v.(type) {
	case string:
		return t != ""
	case []any:
		return len(t) > 0
	case int:
		return t != 0
	}
	return v != nil
}

// addLine is the body of every *_TEXT rule: a line indented by the export's
// three spaces opens a new line of the field, anything else continues the last.
func (l *dbLexer) addLine(line string) {
	if strings.HasPrefix(line, "   ") {
		l.text += "\n" + line[3:]
	} else {
		l.text += line
	}
}

// closeList ends a field that may have collected numbered items: the last item
// joins the list, or — when nothing was numbered — the field is the plain text.
// A second field of the same name for one question is appended to the first.
func (l *dbLexer) closeList(dst *any) {
	switch cur := (*dst).(type) {
	case []any:
		if len(cur) > 0 {
			*dst = append(cur, l.text)
			return
		}
	case string:
		if cur != "" {
			*dst = cur + "\n" + l.text
			return
		}
	}
	*dst = l.text
}

// dbRules is every state's rule list, in PLY's order. t_ANY_TEXT and
// t_ANY_ENDLINE close each one, being defined last.
var dbRules = map[dbState][]dbRule{}

func init() {
	header := func(word string, s dbState) dbRule {
		return dbRule{regexp.MustCompile(`(?i)^` + word + `:\n`),
			func(l *dbLexer, _ string) { l.enter(s) }}
	}
	// opens is a header that also starts a question: the tour line and each
	// question line both do.
	opens := func(pattern string, s dbState) dbRule {
		return dbRule{regexp.MustCompile(`(?i)^` + pattern), func(l *dbLexer, _ string) {
			l.enter(s)
			l.initQuestion()
		}}
	}
	end := func(typ string, typography bool) dbRule {
		return dbRule{reDBBlankLine, func(l *dbLexer, _ string) {
			v := any(l.text)
			if typography {
				v = recursiveTypography(v, l.opts)
			}
			l.push(typ, v)
			l.goTo(dbInitial)
		}}
	}
	// field is a one-value state: whatever it read is the value.
	field := func(dst func(*dbLexer) *string) []dbRule {
		return plainDBRules(dbRule{reDBBlankLine, func(l *dbLexer, _ string) {
			*dst(l) = typo.Typography(l.text, l.opts)
			l.goTo(dbInitial)
		}})
	}
	// listed is a state whose value may be a numbered list: answer and source.
	listed := func(dst func(*dbLexer) *any, toList bool) []dbRule {
		return plainDBRules(
			dbRule{reDBLine, func(l *dbLexer, m string) {
				item := reDBListItem.FindStringSubmatch(m)
				if item == nil {
					l.addLine(m)
					return
				}
				if toList {
					// Bad format: a plain source, then a numbered one.
					if s, wasString := (*dst(l)).(string); wasString {
						*dst(l) = []any{s}
					}
				}
				if l.text != "" {
					*dst(l) = append((*dst(l)).([]any), l.text)
				}
				l.text = item[2]
			}},
			dbRule{reDBBlankLine, func(l *dbLexer, _ string) {
				l.closeList(dst(l))
				*dst(l) = recursiveTypography(*dst(l), l.opts)
				l.goTo(dbInitial)
			}},
		)
	}

	dbRules[dbInitial] = plainDBRules(
		header("Чемпионат", dbTitle),
		header("URL", dbURL),
		header("Дата", dbDate),
		header("Редактор", dbEditor),
		header("Инфо", dbInfo),
		opens(`Тур:\n`, dbTour),
		opens(`Вопрос`+ws+`+\d+:\n`, dbQuestion),
		header("Ответ", dbAnswer),
		header("Зачет", dbZachet),
		header("Незачет", dbNezachet),
		header("Комментарий", dbComment),
		header("Источник", dbSource),
		header("Автор", dbAuthor),
	)

	dbRules[dbTitle] = plainDBRules(end("heading", true))
	// A URL and a date are filed as they stand: the two fields with no typography.
	dbRules[dbURL] = plainDBRules(end("meta", false))
	dbRules[dbDate] = plainDBRules(end("date", false))
	dbRules[dbInfo] = plainDBRules(end("meta", true))
	dbRules[dbEditor] = plainDBRules(end("editor", true))
	dbRules[dbTour] = plainDBRules(end("tour", true))

	dbRules[dbZachet] = field(func(l *dbLexer) *string { return &l.cur().zachet })
	dbRules[dbNezachet] = field(func(l *dbLexer) *string { return &l.cur().nezachet })
	dbRules[dbAuthor] = field(func(l *dbLexer) *string { return &l.cur().author })
	dbRules[dbAnswer] = listed(func(l *dbLexer) *any { return &l.cur().answer }, false)
	dbRules[dbSource] = listed(func(l *dbLexer) *any { return &l.cur().source }, true)

	dbRules[dbQuestion] = plainDBRules(
		dbRule{regexp.MustCompile(`(?i)^` + ws + `{3}<раздатка>\n`), func(l *dbLexer, _ string) {
			l.text += "[Раздаточный материал:"
			l.goTo(dbHandout)
		}},
		dbRule{regexp.MustCompile(`(?i)^(?:\((?:img|aud)` + ws + `(?:[\d\.\w]+)\)` + ws + `*)+\n`),
			func(l *dbLexer, m string) {
				l.text += "[Раздаточный материал:" + strings.TrimSpace(m) + "]"
			}},
		dbRule{reDBLine, func(l *dbLexer, m string) {
			item := reDBListItem.FindStringSubmatch(m)
			if item == nil {
				l.addLine(m)
				return
			}
			if l.text != "" {
				l.cur().question = appendSubQuestion(l.cur().question, l.text)
			}
			l.text = item[2]
		}},
		dbRule{reDBBlankLine, func(l *dbLexer, _ string) {
			q := l.cur()
			// The two-element shape is a blitz: a preamble, then its sub-questions.
			if list, ok := q.question.([]any); ok && len(list) == 2 {
				q.question = appendSubQuestion(list, l.text)
			} else {
				q.question = l.text
			}
			q.question = recursiveTypography(q.question, l.opts)
			l.goTo(dbInitial)
		}},
	)

	dbRules[dbHandout] = plainDBRules(
		dbRule{regexp.MustCompile(`(?i)^` + ws + `{3}</раздатка>\n`), func(l *dbLexer, _ string) {
			l.text += "\n]"
			l.goTo(dbQuestion)
		}},
	)

	dbRules[dbComment] = plainDBRules(
		dbRule{reDBLine, func(l *dbLexer, m string) {
			// Only a comment that is still a list reads "1." as an item; once it
			// has opened with prose, the numbers are part of the prose.
			if list, isList := l.cur().comment.([]any); isList {
				if item := reDBListItem.FindStringSubmatch(m); item != nil {
					if l.text != "" {
						l.cur().comment = append(list, l.text)
					}
					l.text = item[2]
					return
				}
				if len(list) == 0 && l.text == "" {
					l.cur().comment = ""
				}
			}
			l.addLine(m)
		}},
		dbRule{reDBBlankLine, func(l *dbLexer, _ string) {
			l.closeList(&l.cur().comment)
			l.cur().comment = recursiveTypography(l.cur().comment, l.opts)
			l.goTo(dbInitial)
		}},
	)
}

// appendSubQuestion grows the [preamble, [sub-questions…]] shape a blitz has.
func appendSubQuestion(v any, text string) any {
	list, _ := v.([]any)
	switch len(list) {
	case 0:
		return append(list, text)
	case 1:
		return append(list, []any{text})
	default:
		sub, _ := list[1].([]any)
		list[1] = append(sub, text)
		return list
	}
}

// plainDBRules closes a state's list with the two rules every state ends with.
func plainDBRules(rules ...dbRule) []dbRule {
	return append(rules,
		dbRule{reDBLine, func(l *dbLexer, m string) { l.addLine(m) }},
		dbRule{reDBNewline, func(l *dbLexer, _ string) { l.text += " " }},
	)
}
