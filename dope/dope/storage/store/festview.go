package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Fest-view read queries: load a fest's venues, per-stage matches, match team
// summaries and reseed entries into the view types above. They read through a
// Queryer so callers can pass a pooled connection or a snapshot transaction.

// LoadVenues returns a fest's venues ordered by number.
func LoadVenues(ctx context.Context, q Queryer, festID int64) ([]VenueView, error) {
	return CollectRows(ctx, q, `
select number, title from venues
where fest_id = ?
order by number`, []any{festID}, func(rows *sql.Rows) (VenueView, error) {
		var venue VenueView
		if err := rows.Scan(&venue.Number, &venue.Title); err != nil {
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
       v.number, v.title
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
		if err := rows.Scan(&matchID, &match.Code, &match.Title, &match.Letter, &match.Position, &match.ParticipantCount, &match.Status, &match.Revision, &venueNumber, &venueTitle); err != nil {
			return nil, err
		}
		if venueNumber.Valid {
			match.Venue = &VenueView{Number: int(venueNumber.Int64), Title: venueTitle.String}
		}
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
//
// A Draw Slot also carries who may be seated in it. The grid's panel needs
// Participant ids to send back, and a match summary carries names alone, so
// the candidates are resolved here rather than on the page.
func LoadMatchSummaries(ctx context.Context, q Queryer, matchID int64, gameType string) ([]MatchParticipantSummary, error) {
	score := "coalesce(r.total, 0)"
	if gameType == "brain" {
		score = "coalesce(cast(r.metrics_json ->> '$.taken' as integer), 0)"
	}
	var gameID int64
	draws := map[int]*SchemeDraw{}
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
			team.Draw = &DrawSlotView{Code: ref.Draw.Code, Seated: seated}
			draws[slotIndex] = ref.Draw
		}
		return team, nil
	})
	if err != nil {
		return nil, err
	}
	for index, draw := range draws {
		if index >= len(teams) {
			continue
		}
		candidates, err := LoadDrawCandidates(ctx, q, gameID, draw)
		if err != nil {
			return nil, err
		}
		teams[index].Draw.Candidates = candidates
	}
	return teams, nil
}

// LoadDrawCandidates resolves a Draw Slot's candidate places to whoever holds
// them now. A place in a Match that is not finished resolves to nobody, so the
// panel offers a choice only once the Round it draws from is played out —
// the same gate every other advancing seat waits on.
func LoadDrawCandidates(ctx context.Context, q Queryer, gameID int64, draw *SchemeDraw) ([]DrawCandidateView, error) {
	if draw == nil || len(draw.Candidates) == 0 {
		return nil, nil
	}
	codes := map[string]bool{}
	args := []any{gameID}
	for _, candidate := range draw.Candidates {
		if !codes[candidate.Match] {
			codes[candidate.Match] = true
			args = append(args, candidate.Match)
		}
	}
	type held struct {
		Code  string
		Place float64
		ID    int64
		Name  string
	}
	rows, err := CollectRows(ctx, q, `
select m.code, mr.place, mr.participant_id, coalesce(p.name, '')
from match_results mr
join matches m on m.id = mr.match_id
join participants p on p.id = mr.participant_id
where m.game_id = ? and m.status = 'finished' and m.code in (`+placeholders(len(args)-1)+`)`,
		args, func(rows *sql.Rows) (held, error) {
			var h held
			return h, rows.Scan(&h.Code, &h.Place, &h.ID, &h.Name)
		})
	if err != nil {
		return nil, err
	}
	byPlace := map[string]held{}
	for _, row := range rows {
		byPlace[fmt.Sprintf("%s:%g", row.Code, row.Place)] = row
	}
	var out []DrawCandidateView
	seen := map[int64]bool{}
	for _, candidate := range draw.Candidates {
		row, ok := byPlace[fmt.Sprintf("%s:%d", candidate.Match, candidate.Place)]
		if !ok || seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		out = append(out, DrawCandidateView{ID: row.ID, Name: row.Name})
	}
	return out, nil
}

// NonEmptyJSON returns "{}" for a blank string, else the trimmed value.
func NonEmptyJSON(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "{}"
	}
	return value
}
