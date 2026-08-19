// The flat formats that predate the DSL — ОД and КСИ — built the way they
// always were, behind the same Spec; and the столы every scheme may name.
package gamebuild

import (
	"context"
	"database/sql"
	"encoding/json"

	"dope/dope/domain/flatgame"
	"dope/dope/domain/games"
	"dope/dope/domain/protocol"
	"dope/dope/domain/roster"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
)

func createODGameTx(ctx context.Context, tx *sql.Tx, festID int64, tours, questions int) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, "od", "ОД")
	if err != nil {
		return 0, err
	}
	tourComp := make([]int, tours)
	for i := range tourComp {
		tourComp[i] = questions
	}
	emptyScheme, emptyState := games.ODEmptyGameJSON(identity.Code, identity.Title, tourComp)
	schemeJSON, stateJSON, err := pristineFlatTx(ctx, tx, festID, games.OD, emptyScheme, emptyState)
	if err != nil {
		return 0, err
	}
	return insertJSONGameTx(ctx, tx, festID, identity, "od", schemeJSON, stateJSON)
}

func createKSIGameTx(ctx context.Context, tx *sql.Tx, festID int64, themesCount int, stickers json.RawMessage) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, "ksi", "КСИ")
	if err != nil {
		return 0, err
	}
	emptyScheme, emptyState := games.KSIStickersEmptyGameJSON(identity.Code, identity.Title, themesCount, stickers)
	schemeJSON, stateJSON, err := pristineFlatTx(ctx, tx, festID, games.KSI, emptyScheme, emptyState)
	if err != nil {
		return 0, err
	}
	return insertJSONGameTx(ctx, tx, festID, identity, "ksi", schemeJSON, stateJSON)
}

// pristineFlatTx is a flat game's empty scheme and state with the фест's
// roster already folded in through its Protocol — what creation and
// «Очистить» write.
func pristineFlatTx(ctx context.Context, tx *sql.Tx, festID int64, gameType string, schemeJSON, stateJSON []byte) ([]byte, []byte, error) {
	teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return nil, nil, err
	}
	if len(teams) == 0 {
		return schemeJSON, stateJSON, nil
	}
	scheme, state, ok, err := protocol.FoldRoster(gameType, string(schemeJSON), string(stateJSON), roster.RosterTeams(teams), nil)
	if err != nil || !ok {
		return schemeJSON, stateJSON, err
	}
	return scheme, state, nil
}

func insertJSONGameTx(ctx context.Context, tx *sql.Tx, festID int64, identity gameIdentity, gameType string, schemeJSON, stateJSON []byte) (int64, error) {
	now := util.UtcNow()
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, uniqueSchemeSlug(identity.Code), identity.Title, string(schemeJSON), now)
	if err != nil {
		return 0, err
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, '{}', 'active', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, identity.Title, gameType, identity.Position, schemeID, string(schemeJSON), now, now)
	if err != nil {
		return 0, err
	}
	return gameID, insertFlatMatchTx(ctx, tx, festID, gameID, identity.Title, string(stateJSON), now)
}

// insertFlatMatchTx writes a flat game's Structure — one flat Block holding
// one 'main' бой that carries the whole document — and settles it: seats
// from the document, scored, ranked (flatgame).
func insertFlatMatchTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, title, stateJSON, now string) error {
	stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json, block_code, wave_index)
values(?, ?, 'main', '', 'matches', 'flat', 1, 'active', '{}', 'main', 1)`, festID, gameID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, round, wave, participant_count, status, revision, state_json)
values(?, ?, ?, 'main', ?, 1, 1, 1, 0, 'active', 0, ?)`, festID, gameID, stageID, title, stateJSON); err != nil {
		return err
	}
	return flatgame.SettleTx(ctx, tx, festID, gameID)
}

// upsertVenuesTx makes the фест's столы the scheme names and returns them by
// number, for the бои to point at.
func upsertVenuesTx(ctx context.Context, tx *sql.Tx, festID int64, venues []store.SchemeVenue) (map[int]int64, error) {
	now := util.UtcNow()
	ids := make(map[int]int64, len(venues))
	for _, venue := range venues {
		id, err := upsertVenueTx(ctx, tx, festID, venue, now)
		if err != nil {
			return nil, err
		}
		ids[venue.Number] = id
	}
	return ids, nil
}

func upsertVenueTx(ctx context.Context, tx *sql.Tx, festID int64, venue store.SchemeVenue, now string) (int64, error) {
	if _, err := tx.ExecContext(ctx, `
insert into venues(fest_id, number, title, created_at, updated_at)
values(?, ?, ?, ?, ?)
on conflict(fest_id, number) do update set title = excluded.title, updated_at = excluded.updated_at`,
		festID, venue.Number, venue.Title, now, now); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, `select id from venues where fest_id = ? and number = ?`, festID, venue.Number).Scan(&id)
	return id, err
}
