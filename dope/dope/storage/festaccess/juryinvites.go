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

// JuryInvites is dopecore/invitelink bound to dope's jury_invites: a link
// names a fest, and following it makes the holder its game_jury, who rules
// on the contested answers of every Game.
func JuryInvites() invitelink.Links {
	return invitelink.Links{
		Invites:   "jury_invites",
		Uses:      "jury_invite_uses",
		ScopeID:   "fest_id",
		ScopeJoin: "join fests f on f.id = i.fest_id",
		Scope:     juryScope{},
	}
}

type juryScope struct{ gameScope }

// IsMember says the person has a role in the fest already. A link never
// changes a role: a host who follows it stays a host.
func (juryScope) IsMember(ctx context.Context, q invitelink.Querier, festID, userID int64) (bool, error) {
	role, err := FestUserRoleFromQuery(ctx, q, festID, userID)
	return role != "", err
}

// AddMember makes a stranger to the fest its jury.
func (juryScope) AddMember(ctx context.Context, tx invitelink.Tx, festID, userID int64) error {
	_, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, ?, ?)
on conflict(fest_id, user_id) do nothing`, festID, userID, roles.GameJury, util.UtcNow())
	return err
}

func (juryScope) DisplayName(ctx context.Context, q invitelink.Querier, festID int64) (string, error) {
	var fest string
	err := q.QueryRowContext(ctx, `select title from fests where id = ?`, festID).Scan(&fest)
	return dopestrings.Default.Festaccess.Invite.JuryScopeName(fest), err
}

// JuryFestID is the fest a jury link names, once it is sure the fest exists.
func JuryFestID(ctx context.Context, q invitelink.Querier, festID int64) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `select id from fests where id = ?`, festID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, invitelink.ErrNotFound
	}
	return id, err
}
