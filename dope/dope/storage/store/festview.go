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
select v.number, v.title, v.hosts, (select count(*) from matches m where m.venue_id = v.id), coalesce(g.title, '')
from venues v
left join games g on g.id = v.game_id
where v.fest_id = ?
order by v.number`, []any{festID}, func(rows *sql.Rows) (VenueView, error) {
		var venue VenueView
		if err := rows.Scan(&venue.Number, &venue.Title, &venue.Hosts, &venue.Bouts, &venue.GameTitle); err != nil {
			return venue, err
		}
		venue.GameOnly = venue.GameTitle != ""
		return venue, nil
	})
}

// LoadGameVenues returns a fest's venues as one Game shows them: each with
// the Game's own title and hosts over the fest's, and how many of the Game's
// bouts play at it.
func LoadGameVenues(ctx context.Context, q Queryer, festID, gameID int64) ([]VenueView, error) {
	return CollectRows(ctx, q, `
select v.number, v.title, v.hosts, gv.title, gv.hosts, coalesce(gv.hidden, 0), v.game_id is not null,
       (select count(*) from matches m where m.venue_id = v.id and m.game_id = ?)
from venues v
left join game_venues gv on gv.venue_id = v.id and gv.game_id = ?
where v.fest_id = ? and (v.game_id is null or v.game_id = ?)
order by v.number`, []any{gameID, gameID, festID, gameID}, func(rows *sql.Rows) (VenueView, error) {
		var venue VenueView
		var ownTitle, ownHosts sql.NullString
		if err := rows.Scan(&venue.Number, &venue.FestTitle, &venue.FestHosts, &ownTitle, &ownHosts, &venue.Hidden, &venue.GameOnly, &venue.Bouts); err != nil {
			return venue, err
		}
		if venue.GameOnly {
			venue.Title, venue.Hosts = venue.FestTitle, venue.FestHosts
			venue.FestTitle, venue.FestHosts = "", ""
			return venue, nil
		}
		venue.Title, venue.Hosts = venue.FestTitle, venue.FestHosts
		if ownTitle.Valid {
			venue.Title, venue.OwnTitle = ownTitle.String, &ownTitle.String
		}
		if ownHosts.Valid {
			venue.Hosts, venue.OwnHosts = ownHosts.String, &ownHosts.String
		}
		return venue, nil
	})
}

// VenueBout is one bout of a fest's schedule at a venue: when it starts, the
// Game it belongs to (its URL ref and title) and the bout's letter, code and
// title.
type VenueBout struct {
	Venue     int    `json:"venue"`
	StartsAt  string `json:"startsAt,omitempty"`
	Game      string `json:"game"`
	GameTitle string `json:"gameTitle"`
	Code      string `json:"code"`
	Letter    string `json:"letter,omitempty"`
	Title     string `json:"title,omitempty"`
	Finished  bool   `json:"finished,omitempty"`
	// Room and Hosts are the Game's own title and hosts for the venue, when
	// it gave them.
	Room  string `json:"room,omitempty"`
	Hosts string `json:"hosts,omitempty"`
}

// LoadVenueBouts returns every bout of the fest that sits at a venue, venue
// by venue, in the order they are played: by start time, then the Game's
// order and the schedule's.
func LoadVenueBouts(ctx context.Context, q Queryer, festID int64) ([]VenueBout, error) {
	return CollectRows(ctx, q, `
select v.number, coalesce(m.starts_at, ''), coalesce(nullif(g.slug, ''), g.code), g.title,
       m.code, m.letter, m.title, m.status = 'finished', coalesce(gv.title, ''), coalesce(gv.hosts, '')
from matches m
join venues v on v.id = m.venue_id
join games g on g.id = m.game_id
left join game_venues gv on gv.game_id = m.game_id and gv.venue_id = m.venue_id
join stages s on s.id = m.stage_id
where m.fest_id = ?
order by v.number, coalesce(m.starts_at, '') = '', m.starts_at, g.position, s.position, m.position`, []any{festID},
		func(rows *sql.Rows) (VenueBout, error) {
			var b VenueBout
			return b, rows.Scan(&b.Venue, &b.StartsAt, &b.Game, &b.GameTitle, &b.Code, &b.Letter, &b.Title, &b.Finished, &b.Room, &b.Hosts)
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
       v.number, coalesce(gv.title, v.title), coalesce(gv.hosts, v.hosts, ''), coalesce(m.starts_at, ''), m.game_id
from matches m
left join venues v on v.id = m.venue_id
left join game_venues gv on gv.game_id = m.game_id and gv.venue_id = m.venue_id
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
		var venueHosts string
		if err := rows.Scan(&matchID, &match.Code, &match.Title, &match.Letter, &match.Position, &match.ParticipantCount, &match.Status, &match.Revision, &venueNumber, &venueTitle, &venueHosts, &match.StartsAt, &match.GameID); err != nil {
			return nil, err
		}
		if venueNumber.Valid {
			match.Venue = &VenueView{Number: int(venueNumber.Int64), Title: venueTitle.String, Hosts: venueHosts}
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
