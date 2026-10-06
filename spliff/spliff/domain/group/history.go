package group

import (
	"encoding/json"

	"spliff/spliff/storage/store"
)

// snapshot is what History keeps of a Transaction: everything a person can
// change, as JSON, so a diff can be shown without a second schema.
//
// The field names are the API's, snake_case, because the page that reads a
// History entry back is the same page that reads a Transaction: one shape to
// know, not two. That is also why the entries are a type of their own rather
// than store.Entry, since a storage struct's field names are nobody's wire
// format.
type snapshot struct {
	Description string          `json:"description"`
	Day         string          `json:"day"`
	Currency    string          `json:"currency"`
	TotalMinor  int64           `json:"total_minor"`
	Payments    []snapshotEntry `json:"payments"`
	Shares      []snapshotEntry `json:"shares"`
	Photos      int             `json:"photos"`
}

type snapshotEntry struct {
	MemberID int64 `json:"member_id"`
	Minor    int64 `json:"minor"`
}

func snapshotEntries(in []store.Entry) []snapshotEntry {
	out := make([]snapshotEntry, 0, len(in))
	for _, e := range in {
		out = append(out, snapshotEntry{MemberID: e.MemberID, Minor: e.Minor})
	}
	return out
}

func snapshotOf(t store.Transaction) string {
	body, err := json.Marshal(snapshot{
		Description: t.Description, Day: t.Day, Currency: t.Currency,
		TotalMinor: t.TotalMinor,
		Payments:   snapshotEntries(t.Payments), Shares: snapshotEntries(t.Shares),
		Photos: len(t.Photos),
	})
	if err != nil {
		return ""
	}
	return string(body)
}

func snapshotOfWrite(w store.Write, photos int) string {
	return snapshotOf(store.Transaction{
		Description: w.Description, Day: w.Day, Currency: w.Currency,
		TotalMinor: w.TotalMinor, Payments: w.Payments, Shares: w.Shares,
		Photos: make([]store.Photo, photos),
	})
}

func withPhotoCount(t store.Transaction, n int) store.Transaction {
	t.Photos = make([]store.Photo, n)
	return t
}
