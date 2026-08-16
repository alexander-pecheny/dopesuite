// Package festview composes the fest view — what every game page draws from
// and what the server broadcasts after each write: the Fest and Game header,
// the venues, and every stage with its grain, its бои, its Ranker's table and
// the sort rules that table shows. One place answers the question, so no page
// re-derives a Block from a code or a column from a config.
package festview

import (
	"context"
	"encoding/json"

	"dope/dope/domain/resolver"
	"dope/dope/domain/structure"
	"dope/dope/storage/store"
)

// Load runs every fest-view query against q — a read-only tx for a WAL
// snapshot off the write lock, or the DB under it.
func Load(ctx context.Context, q store.Queryer, festID, gameID int64) (store.FestView, error) {
	var view store.FestView
	view.QuestionValues = store.QuestionValues
	view.RegularThemeCount = store.ThemeCount
	if festID == 0 {
		view.Slug = ""
		view.Title = ""
		view.UpdatedAt = ""
		return view, nil
	}
	var updatedAt string
	if err := q.QueryRowContext(ctx, `
select coalesce(t.slug, ''), t.title, t.revision, t.updated_at, coalesce(g.scheme_json, ''), coalesce(g.title, ''), coalesce(g.game_type, '')
from fests t
left join games g on g.fest_id = t.id and g.id = ?
where t.id = ?`, gameID, festID).
		Scan(&view.Slug, &view.Title, &view.Revision, &updatedAt, &view.SchemaJSON, &view.GameName, &view.GameType); err != nil {
		return store.FestView{}, err
	}
	view.UpdatedAt = updatedAt

	venues, err := store.LoadVenues(ctx, q, festID)
	if err != nil {
		return store.FestView{}, err
	}
	view.Venues = venues

	stageWhere := "fest_id = ?"
	stageArgs := []any{festID}
	if gameID > 0 {
		stageWhere += " and game_id = ?"
		stageArgs = append(stageArgs, gameID)
	}
	stageRows, err := q.QueryContext(ctx, `
select id, code, title, stage_type, kind, position, status, config_json, block_code, wave_index, group_code
from stages
where `+stageWhere+`
order by position, id`, stageArgs...)
	if err != nil {
		return store.FestView{}, err
	}
	defer stageRows.Close()

	type stageRecord struct {
		ID    int64
		Kind  string
		Stage store.StageView
	}
	var stageRecords []stageRecord
	for stageRows.Next() {
		var stageID int64
		var kind string
		var stage store.StageView
		var configJSON string
		var grain store.SchemeGrain
		if err := stageRows.Scan(&stageID, &stage.Code, &stage.Title, &stage.Type, &kind, &stage.Position, &stage.Status, &configJSON,
			&grain.Block, &grain.Wave, &grain.Group); err != nil {
			return store.FestView{}, err
		}
		stage.Config = json.RawMessage(store.NonEmptyJSON(configJSON))
		if grain.Block != "" {
			stage.Grain = &grain
		}
		stageRecords = append(stageRecords, stageRecord{ID: stageID, Kind: kind, Stage: stage})
	}
	if err := stageRows.Err(); err != nil {
		return store.FestView{}, err
	}
	if err := stageRows.Close(); err != nil {
		return store.FestView{}, err
	}
	for _, record := range stageRecords {
		record.Stage.Kind = record.Kind
		if ranker, ok := structure.RankerFor(record.Kind); ok {
			record.Stage.Sort = ranker.Order(resolver.KindConfig(record.Stage.Config))
		}
		if record.Stage.Type == "reseed" {
			entries, err := store.LoadReseedEntries(ctx, q, record.ID)
			if err != nil {
				return store.FestView{}, err
			}
			record.Stage.ReseedEntries = entries
			state, err := resolver.ReseedPrerequisites(ctx, q, record.Stage.Config, gameID)
			if err != nil {
				return store.FestView{}, err
			}
			record.Stage.ReseedReady = state.Ready
			record.Stage.ReseedPending = state.PendingMatches
			if !state.Ready {
				record.Stage.ReseedMessage = resolver.ReseedNotReadyMessage(state.PendingMatches)
			}
		} else {
			matches, err := store.LoadFestMatches(ctx, q, record.ID, view.GameType)
			if err != nil {
				return store.FestView{}, err
			}
			record.Stage.Matches = matches
			// A Kind that ranks keeps its own table — the Сетка draws a Group as
			// место against team, the way the sheets do, and the бои stay for the
			// tab that lists them.
			if resolver.RanksItsOwnStage(record.Kind) {
				standings, err := store.LoadReseedEntries(ctx, q, record.ID)
				if err != nil {
					return store.FestView{}, err
				}
				record.Stage.Standings = standings
			}
		}
		view.Stages = append(view.Stages, record.Stage)
	}
	return view, nil
}
