package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Shared row helpers used by the journal checkpoint/replay/revert engine to read
// and write arbitrary rows generically (by column map + primary key). Originally
// part of the audit-revert path; kept here as the audit_log machinery is retired.

// jsonToSQLValue maps a decoded JSON value to a SQLite bind value, keeping
// integers as int64 (not float64) so ids and counts round-trip exactly.
func jsonToSQLValue(v any) any {
	switch n := v.(type) {
	case nil:
		return nil
	case json.Number:
		str := n.String()
		if !strings.ContainsAny(str, ".eE") {
			if i, err := strconv.ParseInt(str, 10, 64); err == nil {
				return i
			}
		}
		if f, err := n.Float64(); err == nil {
			return f
		}
		return str
	default:
		return v
	}
}

func insertRow(ctx context.Context, tx *sql.Tx, table string, row map[string]any) error {
	cols := sortedKeys(row)
	if len(cols) == 0 {
		return errors.New("empty row")
	}
	placeholders := make([]string, len(cols))
	args := make([]any, len(cols))
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quoteIdent(c)
		placeholders[i] = "?"
		args[i] = row[c]
	}
	// INSERT OR IGNORE: if the row already exists (e.g. a checkpoint restore that
	// a cascade already recreated), don't fail the whole restore.
	query := fmt.Sprintf("insert or ignore into %s (%s) values (%s)",
		table, strings.Join(quoted, ", "), strings.Join(placeholders, ", "))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func updateRowByPK(ctx context.Context, tx *sql.Tx, table string, pks []string, row map[string]any) error {
	if len(pks) == 0 {
		return errors.New("table has no primary key")
	}
	pkSet := map[string]bool{}
	for _, p := range pks {
		pkSet[p] = true
	}
	setCols := make([]string, 0, len(row))
	args := make([]any, 0, len(row)+len(pks))
	for _, c := range sortedKeys(row) {
		if pkSet[c] {
			continue
		}
		setCols = append(setCols, quoteIdent(c)+" = ?")
		args = append(args, row[c])
	}
	if len(setCols) == 0 {
		return nil
	}
	where, whereArgs, err := pkWhere(pks, row)
	if err != nil {
		return err
	}
	args = append(args, whereArgs...)
	query := fmt.Sprintf("update %s set %s where %s", table, strings.Join(setCols, ", "), where)
	_, err = tx.ExecContext(ctx, query, args...)
	return err
}

func deleteByPK(ctx context.Context, tx *sql.Tx, table string, pks []string, row map[string]any) error {
	where, whereArgs, err := pkWhere(pks, row)
	if err != nil {
		return err
	}
	query := fmt.Sprintf("delete from %s where %s", table, where)
	_, err = tx.ExecContext(ctx, query, whereArgs...)
	return err
}

func pkWhere(pks []string, row map[string]any) (string, []any, error) {
	if len(pks) == 0 {
		return "", nil, errors.New("table has no primary key")
	}
	clauses := make([]string, len(pks))
	args := make([]any, len(pks))
	for i, p := range pks {
		v, ok := row[p]
		if !ok {
			return "", nil, fmt.Errorf("row json missing pk column %q", p)
		}
		clauses[i] = quoteIdent(p) + " = ?"
		args[i] = v
	}
	return strings.Join(clauses, " and "), args, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
