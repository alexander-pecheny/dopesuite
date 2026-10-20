package festaccess

import (
	"context"
	"database/sql"
	"errors"

	"dope/dope/platform/roles"
	"dope/dope/platform/util"
	dopestrings "dope/i18nstrings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/invitelink"
)

// VenueInvites is dopecore/invitelink bound to dope's venue_invites: a link
// names one venue of a synch or multi fest, and following it makes the holder
// a host of that fest limited to the venue (ADR-0032).
func VenueInvites() invitelink.Links {
	return invitelink.Links{
		Invites:   "venue_invites",
		Uses:      "venue_invite_uses",
		ScopeID:   "multifest_venue_id",
		ScopeJoin: "join multifest_venues v on v.id = i.multifest_venue_id",
		Scope:     venueScope{},
	}
}

type venueScope struct{ gameScope }

// IsMember says the person already runs this venue: an admin, the creator, a
// host nobody limited, or a host limited to it among others. A link never
// narrows anyone, so a host nobody limited stays so.
func (venueScope) IsMember(ctx context.Context, q invitelink.Querier, venueID, userID int64) (bool, error) {
	festID, err := VenueFestID(ctx, q, venueID)
	if err != nil {
		return false, err
	}
	role, err := FestUserRoleFromQuery(ctx, q, festID, userID)
	if err != nil || role == "" {
		return false, err
	}
	if roles.Normalize(role) != roles.Host {
		return true, nil
	}
	var limited, here int
	err = q.QueryRowContext(ctx, `
select (select count(*) from fest_game_hosts where fest_id = ? and user_id = ?)
     + (select count(*) from fest_venue_hosts where fest_id = ? and user_id = ?),
       (select count(*) from fest_venue_hosts where multifest_venue_id = ? and user_id = ?)`,
		festID, userID, festID, userID, venueID, userID).Scan(&limited, &here)
	return limited == 0 || here > 0, err
}

// AddMember runs only for someone IsMember turned away: a stranger to the fest,
// who becomes a host, or a limited host, who gets this venue too.
func (venueScope) AddMember(ctx context.Context, tx invitelink.Tx, venueID, userID int64) error {
	festID, err := VenueFestID(ctx, tx, venueID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, ?, ?)
on conflict(fest_id, user_id) do nothing`, festID, userID, roles.Host, util.UtcNow()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
insert or ignore into fest_venue_hosts(fest_id, multifest_venue_id, user_id) values(?, ?, ?)`, festID, venueID, userID)
	return err
}

func (venueScope) DisplayName(ctx context.Context, q invitelink.Querier, venueID int64) (string, error) {
	var fest, venue string
	err := q.QueryRowContext(ctx, `
select f.title, v.title from multifest_venues v join fests f on f.id = v.fest_id where v.id = ?`, venueID).Scan(&fest, &venue)
	return dopestrings.Default.Festaccess.Invite.VenueScopeName(venue, fest), err
}

// VenueFestID is the fest a venue belongs to.
func VenueFestID(ctx context.Context, q invitelink.Querier, venueID int64) (int64, error) {
	var festID int64
	err := q.QueryRowContext(ctx, `select fest_id from multifest_venues where id = ?`, venueID).Scan(&festID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, invitelink.ErrNotFound
	}
	return festID, err
}
