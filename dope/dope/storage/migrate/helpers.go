package migrate

// rowInt64 reads an integer column from a column→value row map, coercing the
// float64 the JSON/SQL decoders may hand back. Returns 0 when absent or
// non-numeric. (A local copy of the server's identically-named helper, kept here
// so this migration package stays a self-contained leaf.)
func rowInt64(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}
