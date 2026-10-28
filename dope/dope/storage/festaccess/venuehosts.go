package festaccess

import (
	"context"
	"database/sql"
	"errors"

	"dope/dope/platform/roles"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// A host limited to venues of a synch or multi fest (ADR-0032) runs the Games
// on them, as a host limited to Games runs those, and also edits the teams
// and numbers of those venues, which no other host may.

// setHostVenuesTx replaces the venues a host is limited to with venueIDs; none
// lifts the limit. An id that is not a venue of this fest is ignored.
func setHostVenuesTx(ctx context.Context, tx *sql.Tx, festID, userID int64, venueIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `delete from fest_venue_hosts where fest_id = ? and user_id = ?`, festID, userID); err != nil {
		return err
	}
	for _, venueID := range venueIDs {
		if _, err := tx.ExecContext(ctx, `
insert or ignore into fest_venue_hosts(fest_id, multifest_venue_id, user_id)
select fest_id, id, ? from multifest_venues where id = ? and fest_id = ?`, userID, venueID, festID); err != nil {
			return err
		}
	}
	return nil
}

// HostVenuesByUser lists, per host of the fest, the venues they are limited to.
func HostVenuesByUser(ctx context.Context, q store.Queryer, festID int64) (map[int64][]int64, error) {
	rows, err := store.CollectRows(ctx, q, `
select user_id, multifest_venue_id from fest_venue_hosts where fest_id = ? order by user_id, multifest_venue_id`,
		[]any{festID}, func(rs *sql.Rows) ([2]int64, error) {
			var pair [2]int64
			return pair, rs.Scan(&pair[0], &pair[1])
		})
	if err != nil {
		return nil, err
	}
	out := map[int64][]int64{}
	for _, pair := range rows {
		out[pair[0]] = append(out[pair[0]], pair[1])
	}
	return out, nil
}

// RosterVenues says whose teams a user with this role may edit: every team
// (all) for an admin or the creator, the teams of their venues for a host
// limited to venues, and none for anyone else.
func RosterVenues(ctx context.Context, q store.Queryer, festID, userID int64, role string) (venues []int64, all bool, err error) {
	if roles.CanManageFest(role) {
		return nil, true, nil
	}
	if roles.Normalize(role) != roles.Host {
		return nil, false, nil
	}
	venues, err = store.CollectRows(ctx, q, `
select v.id from fest_venue_hosts h join multifest_venues v on v.id = h.multifest_venue_id
where h.fest_id = ? and h.user_id = ? order by v.position, v.id`, []any{festID, userID}, func(rs *sql.Rows) (int64, error) {
		var id int64
		return id, rs.Scan(&id)
	})
	return venues, false, err
}

// CheckSynchHostsTx refuses a synch fest's access with a host who has no
// venue: such a host can open none of the fest's pages.
func CheckSynchHostsTx(ctx context.Context, tx *sql.Tx, festID int64) error {
	var nickname string
	err := tx.QueryRowContext(ctx, `
select coalesce(nullif(u.username, ''), nullif(u.telegram_username, ''), 'user-' || u.id) from fests f
join fest_organizers o on o.fest_id = f.id and o.role = ?
join users u on u.id = o.user_id
where f.id = ? and f.kind = 'synch'
  and not exists (select 1 from fest_venue_hosts h where h.fest_id = f.id and h.user_id = o.user_id)
order by o.user_id limit 1`, roles.Host, festID).Scan(&nickname)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return corei18n.User(dopestrings.Default.Festaccess.Games.SynchHostNeedsVenue(nickname))
}
