package dopeserver

import "dope/dope/store"

// jsonToSQLValue lives in the store leaf; this wrapper keeps the converter's
// call site terse. (The row insert/update/delete helpers it used to sit beside
// moved into the journal leaf's replay and checkpoint engines.)
func jsonToSQLValue(v any) any { return store.JSONToSQLValue(v) }
