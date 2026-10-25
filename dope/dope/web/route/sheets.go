package route

import (
	"context"
	"encoding/json"
	"slices"

	"dope/dope/domain/edit"
	"dope/dope/domain/games"
	"dope/dope/domain/multifest"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// SheetVenues is the venues a host limited to them may write the sheets of in
// the Scope's Game: their own, when the Game is one of the whole synch fest
// that keeps a sheet per venue; none otherwise.
func SheetVenues(ctx context.Context, q store.Queryer, sc Scope) ([]int64, error) {
	var gameType string
	var venue *int64
	if err := q.QueryRowContext(ctx, `select game_type, multifest_venue_id from games where id = ? and fest_id = ?`, sc.GameID, sc.FestID).Scan(&gameType, &venue); err != nil {
		return nil, err
	}
	if _, ok := games.As[games.VenueSheets](gameType); !ok || venue != nil {
		return nil, nil
	}
	if kind, err := multifest.FestKind(ctx, q, sc.FestID); err != nil || kind != multifest.Synch {
		return nil, err
	}
	venues, all, err := festaccess.RosterVenues(ctx, q, sc.FestID, sc.User.UserID, sc.Role)
	if all {
		return nil, err
	}
	return venues, err
}

// KeepToSheets refuses a patch that writes anything but the sheets of the
// venues the Scope allows, when it allows only some.
func (sc Scope) KeepToSheets(req edit.PatchRequest) error {
	if len(sc.SheetVenues) == 0 {
		return nil
	}
	for _, op := range req.Ops {
		var head, venue string
		if len(op.Path) < 2 || json.Unmarshal(op.Path[0], &head) != nil || head != "venues" || json.Unmarshal(op.Path[1], &venue) != nil {
			return ErrOtherVenue
		}
		id, err := idstr.Parse(venue)
		if err != nil || !slices.Contains(sc.SheetVenues, id) {
			return ErrOtherVenue
		}
	}
	return nil
}
