package festaccess

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"dope/dope/platform/roles"
	"dope/dope/platform/util"
	dopestrings "dope/i18nstrings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/invitelink"
)

// GameInvites is dopecore/invitelink bound to dope's game_invites: a link
// names one Game, and following it makes the holder a host of that Game's fest
// limited to it (ADR-0030). Nobody approves a join, so no owner is nudged.
func GameInvites() invitelink.Links {
	return invitelink.Links{
		Invites:   "game_invites",
		Uses:      "game_invite_uses",
		ScopeID:   "game_id",
		ScopeJoin: "join games g on g.id = i.game_id",
		Scope:     gameScope{},
	}
}

type gameScope struct{}

// IsMember says the person already runs this Game: an admin, the creator, a
// host nobody limited, or a host limited to it among others.
func (gameScope) IsMember(ctx context.Context, q invitelink.Querier, gameID, userID int64) (bool, error) {
	festID, err := GameFestID(ctx, q, gameID)
	if err != nil {
		return false, err
	}
	role, err := FestUserRoleFromQuery(ctx, q, festID, userID)
	if err != nil {
		return false, err
	}
	return MayRunGame(ctx, q, festID, gameID, userID, role)
}

// AddMember runs only for someone IsMember turned away: a stranger to the fest,
// who becomes a host, or a host limited to other Games, who gets this one too.
func (gameScope) AddMember(ctx context.Context, tx invitelink.Tx, gameID, userID int64) error {
	festID, err := GameFestID(ctx, tx, gameID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, ?, ?)
on conflict(fest_id, user_id) do nothing`, festID, userID, roles.Host, util.UtcNow()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
insert or ignore into fest_game_hosts(fest_id, game_id, user_id) values(?, ?, ?)`, festID, gameID, userID)
	return err
}

func (gameScope) DisplayName(ctx context.Context, q invitelink.Querier, gameID int64) (string, error) {
	var fest, game string
	err := q.QueryRowContext(ctx, `
select f.title, g.title from games g join fests f on f.id = g.fest_id where g.id = ?`, gameID).Scan(&fest, &game)
	return dopestrings.Default.Festaccess.Invite.ScopeName(game, fest), err
}

func (gameScope) Names(ctx context.Context, q invitelink.Querier, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, `
select id, coalesce(nullif(username, ''), nullif(telegram_username, ''), 'user-' || id) from users
where id in (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

func (gameScope) NudgeOwner(int64, int64) {}

// GameFestID is the fest a Game belongs to.
func GameFestID(ctx context.Context, q invitelink.Querier, gameID int64) (int64, error) {
	var festID int64
	err := q.QueryRowContext(ctx, `select fest_id from games where id = ?`, gameID).Scan(&festID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, invitelink.ErrNotFound
	}
	return festID, err
}

// InviteTexts is dope's wording for the shared invite edge.
func InviteTexts() invitelink.Texts {
	s := dopestrings.Default
	return invitelink.Texts{
		Revoked:          s.Festaccess.Invite.Revoked(),
		Expired:          s.Festaccess.Invite.Expired(),
		Exhausted:        s.Festaccess.Invite.Exhausted(),
		Declined:         s.Festaccess.Invite.Broken(),
		Spent:            s.Festaccess.Invite.Spent(),
		Broken:           s.Festaccess.Invite.Broken(),
		NotFound:         s.Festaccess.Invite.NotFound(),
		RequestNotFound:  s.Festaccess.Invite.Broken(),
		NoSeatsLeft:      s.Festaccess.Invite.Exhausted(),
		LabelTooLong:     s.Festaccess.Invite.LabelTooLong(),
		LimitsOutOfRange: s.Festaccess.Invite.LimitsOutOfRange(),
		DecisionInvalid:  s.Festaccess.Invite.Broken(),
	}
}
