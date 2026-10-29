package gq

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// The dump is gotquestions' own pg_dump in plain text. Import keeps the four
// tables a question needs, and only the questions that are public on the site.

const schema = `
create table packs (id integer primary key, title text not null, tournament_id integer not null);
create table questions (
  id integer primary key, pack_id integer not null,
  handout text not null, handout_pic text not null, text text not null, answer text not null,
  zachet text not null, nezachet text not null, comment text not null, source text not null
);
create table people (id integer primary key, name text not null);
create table question_authors (question_id integer not null, person_id integer not null, alias text not null);
create index question_authors_question on question_authors(question_id);
`

// copied names the columns read out of each COPY block, in insert order.
var copied = map[string][]string{
	"pack":             {"id", "title", `"tIds"`, "published"},
	"question":         {"id", `"packId"`, `"razdatkaText"`, `"razdatkaPic"`, "text", "answer", "zachet", "nezachet", "comment", "source", `"takenDown"`},
	"person":           {"id", "name"},
	"question_authors": {`"questionId"`, `"personId"`, "alias"},
}

type Counts struct{ Packs, Questions, People, Authors int }

// Import loads a pg_dump into db, which must be empty.
func Import(ctx context.Context, db *sql.DB, dump io.Reader) (Counts, error) {
	var n Counts
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return n, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return n, err
	}
	ins, err := inserts(ctx, tx)
	if err != nil {
		return n, err
	}
	public := map[int64]bool{}
	sc := bufio.NewScanner(dump)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	var table string
	var pick []int
	for sc.Scan() {
		line := sc.Text()
		if table == "" {
			table, pick = copyHeader(line)
			continue
		}
		if line == `\.` {
			table = ""
			continue
		}
		raw := strings.Split(line, "\t")
		row := make([]string, len(pick))
		for i, c := range pick {
			row[i] = unescape(raw[c])
		}
		if err := insertRow(ctx, ins, table, row, public, &n); err != nil {
			return n, fmt.Errorf("%s row %q: %w", table, row[0], err)
		}
	}
	if err := sc.Err(); err != nil {
		return n, err
	}
	return n, tx.Commit()
}

func inserts(ctx context.Context, tx *sql.Tx) (map[string]*sql.Stmt, error) {
	queries := map[string]string{
		"pack":             `insert into packs values (?, ?, ?)`,
		"question":         `insert into questions values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"person":           `insert into people values (?, ?)`,
		"question_authors": `insert into question_authors values (?, ?, ?)`,
	}
	out := map[string]*sql.Stmt{}
	for t, q := range queries {
		st, err := tx.PrepareContext(ctx, q)
		if err != nil {
			return nil, err
		}
		out[t] = st
	}
	return out, nil
}

func insertRow(ctx context.Context, ins map[string]*sql.Stmt, table string, row []string, public map[int64]bool, n *Counts) error {
	var args []any
	switch table {
	case "pack":
		id, _ := idstr.Parse(row[0])
		if row[3] != "t" {
			return nil
		}
		public[id] = true
		tournament, _, _ := strings.Cut(strings.Trim(row[2], "{}"), ",")
		tid, _ := idstr.Parse(tournament)
		args, n.Packs = []any{id, row[1], tid}, n.Packs+1
	case "question":
		pack, _ := idstr.Parse(row[1])
		if !public[pack] || row[10] == "t" {
			return nil
		}
		args, n.Questions = anyOf(row[:10]), n.Questions+1
	case "person":
		args, n.People = anyOf(row), n.People+1
	case "question_authors":
		args, n.Authors = anyOf(row), n.Authors+1
	}
	_, err := ins[table].ExecContext(ctx, args...)
	return err
}

func anyOf(row []string) []any {
	out := make([]any, len(row))
	for i, v := range row {
		out[i] = v
	}
	return out
}

// copyHeader answers the table a COPY line starts and where its wanted
// columns sit, or "" for any other line.
func copyHeader(line string) (string, []int) {
	rest, ok := strings.CutPrefix(line, "COPY public.")
	if !ok {
		return "", nil
	}
	table, cols, _ := strings.Cut(rest, " (")
	want, ok := copied[table]
	if !ok {
		return "", nil
	}
	cols, _, _ = strings.Cut(cols, ")")
	at := map[string]int{}
	for i, c := range strings.Split(cols, ", ") {
		at[c] = i
	}
	pick := make([]int, len(want))
	for i, c := range want {
		pick[i] = at[c]
	}
	return table, pick
}

var copyEscapes = map[byte]byte{'n': '\n', 'r': '\r', 't': '\t', 'b': '\b', 'f': '\f', 'v': '\v', '\\': '\\'}

// unescape reads one field of COPY's text format; \N, a NULL, comes back empty.
func unescape(s string) string {
	if s == `\N` {
		return ""
	}
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			if c, ok := copyEscapes[s[i]]; ok {
				b.WriteByte(c)
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
