package adminusers

import (
	"net/url"
	"slices"
)

// Sort is the ordering of an /admin table. The zero value means the page's own
// default order — whatever its query returns.
type Sort struct {
	Key  string
	Desc bool
}

// ParseSort reads ?sort/?dir, accepting only the keys the page offers; anything
// else is the zero Sort. A key with no direction sorts descending, which is what
// a first click on "biggest" or "most recent" wants.
func ParseSort(q url.Values, keys ...string) Sort {
	key := q.Get("sort")
	if !slices.Contains(keys, key) {
		return Sort{}
	}
	return Sort{Key: key, Desc: q.Get("dir") != "asc"}
}

// Header tells a column heading how to render: the direction its link should ask
// for — flipped when this is already the sorted column — and the arrow that
// marks that column. Non-sorted columns get no arrow.
func (s Sort) Header(key string) (dir, arrow string) {
	if s.Key != key {
		return "desc", ""
	}
	if s.Desc {
		return "asc", " ↓"
	}
	return "desc", " ↑"
}
