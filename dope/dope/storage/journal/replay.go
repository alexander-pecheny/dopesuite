package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"dope/dope/storage/storeutil"
)

// The replay engine applies forward journal records to a database to
// reconstruct state. Replaying a checkpoint's records (or a whole stream from
// genesis) rebuilds the rows as they were at any point — the foundation for
// both historical inspection and derived revert. It understands the generic
// row opcodes (the converter's output and the coarse fallback); an unknown
// opcode is a hard error so a gap can never be silently skipped.

// rowOpJSON is the live row-op payload: {"t":"<table>","r":{<columns>}}.
type rowOpJSON struct {
	Table string         `json:"t"`
	Row   map[string]any `json:"r"`
}

// DecodeRowOpJSON decodes a live JSON row-op payload into its table and a
// SQL-coerced column map.
func DecodeRowOpJSON(payload []byte) (string, map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(string(payload)))
	dec.UseNumber()
	var d rowOpJSON
	if err := dec.Decode(&d); err != nil {
		return "", nil, err
	}
	row := make(map[string]any, len(d.Row))
	for k, v := range d.Row {
		row[k] = storeutil.JSONToSQLValue(v)
	}
	return d.Table, row, nil
}

// Replayer applies forward journal records to a database to reconstruct state.
type Replayer struct {
	dict  map[uint64]string   // dictionary id -> string (table/column names)
	pksBy map[string][]string // table -> primary-key columns (cached from schema)
}

// NewReplayer returns a Replayer over the given id->string dictionary.
func NewReplayer(dict map[uint64]string) *Replayer {
	return &Replayer{dict: dict, pksBy: map[string][]string{}}
}

// LoadDict reads the journal_dict table into an id->string map.
func LoadDict(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (map[uint64]string, error) {
	rows, err := q.QueryContext(ctx, `select id, str from journal_dict`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint64]string{}
	for rows.Next() {
		var id uint64
		var s string
		if err := rows.Scan(&id, &s); err != nil {
			return nil, err
		}
		out[id] = s
	}
	return out, rows.Err()
}

type pkQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// ApplyRowMap applies one decoded row op (insert/update/delete) to tx. Shared by
// the JSON (live) and varint (segment) decode paths.
func (rp *Replayer) ApplyRowMap(ctx context.Context, tx interface {
	pkQuerier
	execer
}, op Op, table string, row map[string]any) error {
	switch op {
	case OpRowIns:
		return replayInsert(ctx, tx, table, row)
	case OpRowSet:
		pks, err := rp.tablePKs(ctx, tx, table)
		if err != nil {
			return err
		}
		return replayUpdate(ctx, tx, table, pks, row)
	case OpRowDel:
		pks, err := rp.tablePKs(ctx, tx, table)
		if err != nil {
			return err
		}
		return replayDelete(ctx, tx, table, pks, row)
	default:
		return fmt.Errorf("journal: applyRowMap on non-row op %s", op)
	}
}

func (rp *Replayer) tablePKs(ctx context.Context, tx pkQuerier, table string) ([]string, error) {
	if pks, ok := rp.pksBy[table]; ok {
		return pks, nil
	}
	rows, err := tx.QueryContext(ctx, `select name, pk from pragma_table_info(`+sqlStringLit(table)+`) order by cid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type pc struct {
		name string
		rank int
	}
	var pkCols []pc
	for rows.Next() {
		var name string
		var pk int
		if err := rows.Scan(&name, &pk); err != nil {
			return nil, err
		}
		if pk > 0 {
			pkCols = append(pkCols, pc{name, pk})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// pragma already orders pk columns by their rank within the cid ordering for
	// single-column keys; sort defensively for composite keys.
	for i := 1; i < len(pkCols); i++ {
		for j := i; j > 0 && pkCols[j-1].rank > pkCols[j].rank; j-- {
			pkCols[j-1], pkCols[j] = pkCols[j], pkCols[j-1]
		}
	}
	pks := make([]string, len(pkCols))
	for i, p := range pkCols {
		pks[i] = p.name
	}
	rp.pksBy[table] = pks
	return pks, nil
}

// RowFromArgs resolves a row op's interned column ids back to a name->value map.
func (rp *Replayer) RowFromArgs(rec Record) (table string, row map[string]any, err error) {
	a, err := DecodeRowArgs(rec.Args)
	if err != nil {
		return "", nil, err
	}
	table, ok := rp.dict[a.TableID]
	if !ok {
		return "", nil, fmt.Errorf("journal: unknown table id %d", a.TableID)
	}
	row = make(map[string]any, len(a.Cols))
	for _, c := range a.Cols {
		name, ok := rp.dict[c.NameID]
		if !ok {
			return "", nil, fmt.Errorf("journal: unknown column id %d", c.NameID)
		}
		row[name] = c.Val
	}
	return table, row, nil
}

// apply applies a single record to tx. tx must satisfy both query (for pk
// introspection) and exec.
func (rp *Replayer) apply(ctx context.Context, tx interface {
	pkQuerier
	execer
}, rec Record) error {
	switch rec.Op {
	case OpRowIns, OpRowSet, OpRowDel:
		return rp.applyRowOp(ctx, tx, rec)
	default:
		return fmt.Errorf("journal: no replay applier for opcode %s (%d)", rec.Op, rec.Op)
	}
}

func (rp *Replayer) applyRowOp(ctx context.Context, tx interface {
	pkQuerier
	execer
}, rec Record) error {
	table, row, err := rp.RowFromArgs(rec)
	if err != nil {
		return err
	}
	return rp.ApplyRowMap(ctx, tx, rec.Op, table, row)
}

// ApplyAll replays records in order.
func (rp *Replayer) ApplyAll(ctx context.Context, tx interface {
	pkQuerier
	execer
}, recs []Record) error {
	for i := range recs {
		if err := rp.apply(ctx, tx, recs[i]); err != nil {
			return fmt.Errorf("replay seq %d: %w", recs[i].Seq, err)
		}
	}
	return nil
}

func replayInsert(ctx context.Context, tx execer, table string, row map[string]any) error {
	cols := storeutil.SortedKeys(row)
	if len(cols) == 0 {
		return fmt.Errorf("replay: empty insert row for %s", table)
	}
	quoted := make([]string, len(cols))
	ph := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, c := range cols {
		quoted[i] = storeutil.QuoteIdent(c)
		ph[i] = "?"
		args[i] = row[c]
	}
	// insert or replace: forward replay from genesis should see a fresh pk, but
	// this keeps replay idempotent if re-run over existing state.
	q := fmt.Sprintf("insert or replace into %s (%s) values (%s)", table, strings.Join(quoted, ", "), strings.Join(ph, ", "))
	_, err := tx.ExecContext(ctx, q, args...)
	return err
}

func replayUpdate(ctx context.Context, tx execer, table string, pks []string, row map[string]any) error {
	if len(pks) == 0 {
		return fmt.Errorf("replay: update on pk-less table %s", table)
	}
	pkSet := map[string]bool{}
	for _, p := range pks {
		pkSet[p] = true
	}
	setCols := make([]string, 0, len(row))
	args := make([]any, 0, len(row)+len(pks))
	for _, c := range storeutil.SortedKeys(row) {
		if pkSet[c] {
			continue
		}
		setCols = append(setCols, storeutil.QuoteIdent(c)+" = ?")
		args = append(args, row[c])
	}
	if len(setCols) == 0 {
		return nil // pk-only update is a no-op
	}
	where, whereArgs, err := storeutil.PKWhere(pks, row)
	if err != nil {
		return err
	}
	args = append(args, whereArgs...)
	q := fmt.Sprintf("update %s set %s where %s", table, strings.Join(setCols, ", "), where)
	_, err = tx.ExecContext(ctx, q, args...)
	return err
}

func replayDelete(ctx context.Context, tx execer, table string, pks []string, row map[string]any) error {
	// Prefer the declared primary key; fall back to all provided columns.
	keyCols := pks
	if len(keyCols) == 0 {
		keyCols = storeutil.SortedKeys(row)
	}
	where, whereArgs, err := storeutil.PKWhere(keyCols, row)
	if err != nil {
		return err
	}
	q := fmt.Sprintf("delete from %s where %s", table, where)
	_, err = tx.ExecContext(ctx, q, whereArgs...)
	return err
}

// sqlStringLit renders a single-quoted SQL string literal (for identifiers
// passed to pragma_table_info, which cannot be parameterized).
func sqlStringLit(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
