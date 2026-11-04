package server

import (
	"context"
	"net/url"
	"strconv"
)

// Board revisions (ADR-0023). Every change to a board's cards and comments
// increments boards.rev and stamps the changed row with it; the triggers that
// do it are revTriggers in db.go. A client that holds the board as of some rev
// sends it as ?since= and gets back only the rows stamped after it.

// fullSnapshot is the since of a full read. Every rev is at least 0, so
// "rev > fullSnapshot" holds for every row.
const fullSnapshot int64 = -1

// boardRev is where a board's numbering stands: rev is its latest change, and
// reaped is the highest rev of a deleted card or comment that the reaper has
// removed for good.
type boardRev struct {
	rev    int64
	reaped int64
}

func readBoardRev(ctx context.Context, q querier, bid int64) (boardRev, error) {
	var br boardRev
	err := q.QueryRowContext(ctx, `select rev, reaped_rev from boards where id = ?`, bid).Scan(&br.rev, &br.reaped)
	return br, err
}

// deltaBase is the rev a delta can start from, or fullSnapshot when the
// request asks for none or one cannot be served. A since below reaped may miss
// a deletion whose tombstone is gone. A since above rev comes from a database
// that has since been restored from a backup.
func (br boardRev) deltaBase(query url.Values) int64 {
	raw := query.Get("since")
	if raw == "" {
		return fullSnapshot
	}
	since, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || since < br.reaped || since > br.rev {
		return fullSnapshot
	}
	return since
}

// scanChangedCards lists the board's cards stamped after since: a live one in
// full when its content changed, or with only its position when it merely
// moved or changed its sets, and a deleted one as an id in gone.
func scanChangedCards(ctx context.Context, q querier, bid, since int64) (changed []cardDTO, gone []int64, err error) {
	rows, err := q.QueryContext(ctx, `
select id, list_id, kind, description, rank, handout_meta, alias, seen, created_at,
       deleted_at is not null, content_rev > ?
from cards where board_id = ? and rev > ? order by rank`, since, bid, since)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	changed, gone = []cardDTO{}, []int64{}
	for rows.Next() {
		var c cardDTO
		var desc, handoutMeta, alias, seen []byte
		var deleted, content bool
		if err := rows.Scan(&c.ID, &c.ListID, &c.Kind, &desc, &c.Rank, &handoutMeta, &alias, &seen, &c.CreatedAt, &deleted, &content); err != nil {
			return nil, nil, err
		}
		switch {
		case deleted:
			gone = append(gone, c.ID)
		case content:
			fillCardContent(&c, desc, handoutMeta, alias, seen)
			changed = append(changed, c)
		default:
			changed = append(changed, cardDTO{ID: c.ID, ListID: c.ListID, Rank: c.Rank, Same: true})
		}
	}
	return changed, gone, rows.Err()
}

// optB64 is b64 for a nullable column: nil stays nil.
func optB64(b []byte) *string {
	if b == nil {
		return nil
	}
	s := b64(b)
	return &s
}
