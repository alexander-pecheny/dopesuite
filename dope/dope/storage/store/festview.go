package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// Fest-view read queries: load a fest's venues, per-stage matches, match team
// summaries and reseed entries into the view types above. They read through a
// Queryer so callers can pass a pooled connection or a snapshot transaction.

// LoadVenues returns a fest's venues ordered by number.
func LoadVenues(ctx context.Context, q Queryer, festID int64) ([]VenueView, error) {
	return CollectRows(ctx, q, `
select v.number, v.title, (select count(*) from matches m where m.venue_id = v.id) from venues v
where v.fest_id = ?
order by v.number`, []any{festID}, func(rows *sql.Rows) (VenueView, error) {
		var venue VenueView
		if err := rows.Scan(&venue.Number, &venue.Title, &venue.Bouts); err != nil {
			return venue, err
		}
		return venue, nil
	})
}

// LoadReseedEntries returns a stage's reseed entries ordered by rank.
func LoadReseedEntries(ctx context.Context, q Queryer, stageID int64) ([]ReseedEntryView, error) {
	return CollectRows(ctx, q, `
select re.rank, re.participant_id, coalesce(t.name, ''), re.metrics_json
from stage_standings re
left join participants t on t.id = re.participant_id
where re.stage_id = ?
order by re.rank`, []any{stageID}, func(rows *sql.Rows) (ReseedEntryView, error) {
		var entry ReseedEntryView
		var metricsJSON string
		if err := rows.Scan(&entry.Rank, &entry.ParticipantID, &entry.Name, &metricsJSON); err != nil {
			return entry, err
		}
		entry.Metrics = json.RawMessage(NonEmptyJSON(metricsJSON))
		return entry, nil
	})
}

// LoadFestMatches returns a stage's matches (with venue and team summaries)
// ordered by position.
func LoadFestMatches(ctx context.Context, q Queryer, stageID int64, gameType string) ([]FestMatchView, error) {
	rows, err := q.QueryContext(ctx, `
select m.id, m.code, m.title, m.letter, m.position, m.participant_count, m.status, m.revision,
       v.number, v.title, coalesce(m.starts_at, ''), m.game_id
from matches m
left join venues v on v.id = m.venue_id
where m.stage_id = ?
order by m.position, m.id`, stageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type matchRecord struct {
		ID    int64
		Match FestMatchView
	}
	var records []matchRecord
	for rows.Next() {
		var matchID int64
		var match FestMatchView
		var venueNumber sql.NullInt64
		var venueTitle sql.NullString
		if err := rows.Scan(&matchID, &match.Code, &match.Title, &match.Letter, &match.Position, &match.ParticipantCount, &match.Status, &match.Revision, &venueNumber, &venueTitle, &match.StartsAt, &match.GameID); err != nil {
			return nil, err
		}
		if venueNumber.Valid {
			match.Venue = &VenueView{Number: int(venueNumber.Int64), Title: venueTitle.String}
		}
		match.ID = matchID
		records = append(records, matchRecord{ID: matchID, Match: match})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	var matches []FestMatchView
	for _, record := range records {
		teams, err := LoadMatchSummaries(ctx, q, record.ID, gameType)
		if err != nil {
			return nil, err
		}
		record.Match.Participants = teams
		matches = append(matches, record.Match)
	}
	return matches, nil
}

// LoadMatchSummaries returns the per-team summary rows for a match, ordered by
// slot index, resolving each slot's source label. The score is what the sheet
// prints as the match's score, and that is not the same column in every game:
// brain counts the questions a side took, everything else scores points.
// The Protocol says which (store.ScoreMetric).
//
// A Draw Slot carries its declared Draw; domain/festview resolves who may be
// seated in it (resolver.DrawOptions), since the grid's panel needs
// Participant ids to send back and a match summary carries names alone.
func LoadMatchSummaries(ctx context.Context, q Queryer, matchID int64, gameType string) ([]MatchParticipantSummary, error) {
	score := "coalesce(r.total, 0)"
	if metric := ScoreMetric(gameType); metric != "" {
		score = "coalesce(cast(r.metrics_json ->> '$." + metric + "' as integer), 0)"
	}
	var gameID int64
	teams, err := CollectRows(ctx, q, `
select t.name, coalesce(ms.participant_id, 0), m.game_id, ms.slot_index, ms.source_type, ms.source_ref_json,
       coalesce(r.place, 0), `+score+`, coalesce(r.plus, 0), coalesce(r.tiebreak, 0)
from match_slots ms
join matches m on m.id = ms.match_id
left join participants t on t.id = ms.participant_id
left join match_results r on r.match_id = ms.match_id and r.participant_id = ms.participant_id
where ms.match_id = ?
order by ms.slot_index`, []any{matchID}, func(rows *sql.Rows) (MatchParticipantSummary, error) {
		var team MatchParticipantSummary
		var name sql.NullString
		var sourceRef string
		var seated int64
		var slotIndex int
		if err := rows.Scan(&name, &seated, &gameID, &slotIndex, &team.SourceType, &sourceRef,
			&team.Place, &team.Total, &team.Plus, &team.Tiebreak); err != nil {
			return team, err
		}
		ref := ParseSlotRef(team.SourceType, sourceRef)
		team.Source = ref.DisplayLabel()
		if name.Valid && name.String != "" {
			team.Name = name.String
		} else {
			team.Name = team.Source
		}
		if ref.Draw != nil {
			team.Draw = &DrawSlotView{Code: ref.Draw.Code, Seated: seated, Apart: ref.Draw.Apart, Draw: ref.Draw}
		}
		return team, nil
	})
	if err != nil {
		return nil, err
	}
	return teams, nil
}

// AdvancePlaceSQL is the place a match_results row (mr) goes forward from: the
// Protocol's advance_place where it writes one — Hamsa's, where teams level on
// the score share a place and the host's lot orders them — and the place
// itself for every other Protocol. A 0 is a shared place nobody has drawn yet,
// which matches no seat.
const AdvancePlaceSQL = `coalesce(json_extract(mr.metrics_json, '$.advance_place'), mr.place)`

// NonEmptyJSON returns "{}" for a blank string, else the trimmed value.
func NonEmptyJSON(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "{}"
	}
	return value
}
