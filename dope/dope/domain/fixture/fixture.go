// Package fixture builds the fest the screenshot matrix is shot against: one
// public fest, one game of every format dope draws a page for, and every
// document written to a fixed arithmetic pattern rather than replayed from a
// recorded tournament. That is the whole point of it — a replay costs half a
// minute and ties the goldens to somebody's championship, while this costs a
// second and renders the same pixels on every machine, so `just matrix` can
// run on a commit instead of before a merge.
//
// It builds the fest the way a host does — gamebuild.Create, the Protocols'
// own documents, the scorer, the resolver — so what the goldens show is what
// dope produces, not a hand-laid pile of rows. The cast and the schemes are
// committed data beside this file: Russian names belong in data, not in a .go
// (root ADR-0006).
package fixture

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
)

//go:embed data/roster.json
var rosterJSON []byte

//go:embed data/ek.dsl
var ekDSL string

//go:embed data/brain.dsl
var brainDSL string

//go:embed data/si.dsl
var siDSL string

//go:embed data/troika.dsl
var troikaDSL string

//go:embed data/hamsa.dsl
var hamsaDSL string

const (
	lineupSize = 4
	// hamsaSeats is what the group stage plays: twelve of the registry, four to
	// a table, so the three places left over are a Draw rather than a fourth
	// table's worth of derived seats.
	hamsaSeats    = 12
	ksiThemes     = 20
	stickerThemes = 12
	odTours       = 3
	odQuestions   = 15
	multiColumns  = 6
)

// Options names the fest to build. Slug is what the matrix's page list says,
// so changing it changes every golden's URL.
type Options struct {
	Slug  string
	Owner int64
}

// cast is the committed roster and the fest's own title.
type cast struct {
	Title string `json:"title"`
	Teams []struct {
		Name string `json:"name"`
		City string `json:"city"`
	} `json:"teams"`
	Players []string `json:"players"`
}

// Build writes the fest and returns its id. Everything happens in one
// transaction: a half-built fixture is worse than none.
func Build(ctx context.Context, db *sql.DB, opts Options) (int64, error) {
	var people cast
	if err := json.Unmarshal(rosterJSON, &people); err != nil {
		return 0, fmt.Errorf("fixture roster: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	festID, err := insertFest(ctx, tx, opts, people.Title)
	if err != nil {
		return 0, err
	}
	teams, err := registerTeams(ctx, tx, festID, people)
	if err != nil {
		return 0, err
	}
	players, err := registerPlayers(ctx, tx, festID, people.Players)
	if err != nil {
		return 0, err
	}

	// The bracket formats, each seating the registry its format plays.
	for _, g := range []struct {
		gameType, dsl string
		entrants      []int64
	}{
		{games.EK, ekDSL, teams},
		{games.Brain, brainDSL, teams},
		{games.SI, siDSL, players},
		{games.Troika, troikaDSL, teams},
		{games.Hamsa, hamsaDSL, teams[:hamsaSeats]},
	} {
		gameID, err := createGame(ctx, tx, gamebuild.Spec{
			FestID: festID, Type: g.gameType, Label: games.Label(g.gameType),
			DSL: g.dsl, Entrants: g.entrants,
		})
		if err != nil {
			return 0, fmt.Errorf("fixture %s: %w", g.gameType, err)
		}
		if err := playBracket(ctx, tx, festID, gameID, g.gameType); err != nil {
			return 0, fmt.Errorf("fixture %s: %w", g.gameType, err)
		}
	}

	// The flat formats seat the fest's roster themselves; only their one
	// document is written.
	for _, build := range []func(context.Context, *sql.Tx, int64) error{
		buildOD, buildKSI, buildStickerKSI, buildMulti,
	} {
		if err := build(ctx, tx, festID); err != nil {
			return 0, err
		}
	}
	return festID, tx.Commit()
}

// createGame makes a game and pins its random seed before anything is played
// on it. A Block separates entrants tied on every metric by a lot drawn from
// games.random_seed, which is a random blob a trigger writes at creation — so
// two seedings of the same fixture send different teams on and a golden of the
// page would never agree with itself. The tie is legitimate; the randomness is
// what a screenshot cannot have, so every game gets a seed of its own name.
func createGame(ctx context.Context, tx *sql.Tx, spec gamebuild.Spec) (int64, error) {
	gameID, err := gamebuild.Create(ctx, tx, spec)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `update games set random_seed = 'fixture-' || code where id = ?`, gameID)
	return gameID, err
}

func insertFest(ctx context.Context, tx *sql.Tx, opts Options, title string) (int64, error) {
	now := util.UtcNow()
	// Public: every page the matrix shoots is a viewer page, and the viewer
	// routes admit only a public fest.
	festID, err := store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, revision, created_at, updated_at, created_by, is_public)
values(?, ?, 1, ?, ?, ?, 1)`, opts.Slug, title, now, now, opts.Owner)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, 'creator', ?)`,
		festID, opts.Owner, now)
	return festID, err
}

// registerTeams writes both team spaces the fest keeps: the registry a scheme
// game seats its entrants from, and the fest_teams family the flat formats
// fold and the roster tab reads — under one numbering, because game creation
// reconciles the two BY NUMBER.
func registerTeams(ctx context.Context, tx *sql.Tx, festID int64, people cast) ([]int64, error) {
	playerIDs, err := registerFestPlayers(ctx, tx, festID, people.Players)
	if err != nil {
		return nil, err
	}
	// The Participants' own people. A format that records who played a theme —
	// Hamsa does — resolves the id through this list, so without it a sheet
	// names nobody and the statistics tab has nothing to count.
	participantPlayers, err := registerParticipantPlayers(ctx, tx, festID, people.Players)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(people.Teams))
	for i, team := range people.Teams {
		number := i + 1
		id, err := store.InsertReturningID(ctx, tx, `
insert into participants(fest_id, roster, name, city, number) values(?, 'team', ?, ?, ?)`,
			festID, team.Name, team.City, number)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
		for seat := 0; seat < lineupSize; seat++ {
			playerID := participantPlayers[(i*lineupSize+seat)%len(participantPlayers)]
			if _, err := tx.ExecContext(ctx, `
insert into participant_players(participant_id, player_id, roster_order) values(?, ?, ?)`,
				id, playerID, seat); err != nil {
				return nil, err
			}
		}
		teamID, err := store.InsertReturningID(ctx, tx, `
insert into fest_teams(fest_id, name, city, position, number) values(?, ?, ?, ?, ?)`,
			festID, team.Name, team.City, number, number)
		if err != nil {
			return nil, err
		}
		// Four of the cast per team, walking the list so no two teams field
		// the same four and the roster tab has something to show.
		for seat := 0; seat < lineupSize; seat++ {
			playerID := playerIDs[(i*lineupSize+seat)%len(playerIDs)]
			if _, err := tx.ExecContext(ctx, `
insert into fest_team_players(team_id, player_id, roster_order) values(?, ?, ?)`,
				teamID, playerID, seat); err != nil {
				return nil, err
			}
		}
	}
	return ids, nil
}

// registerParticipantPlayers writes the people a Participant fields — the
// `players` table, which is what a Match's roster is read from.
func registerParticipantPlayers(ctx context.Context, tx *sql.Tx, festID int64, names []string) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	for _, name := range names {
		first, last, _ := strings.Cut(name, " ")
		id, err := store.InsertReturningID(ctx, tx, `
insert into players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, first, last)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func registerFestPlayers(ctx context.Context, tx *sql.Tx, festID int64, names []string) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	for _, name := range names {
		first, last, _ := strings.Cut(name, " ")
		id, err := store.InsertReturningID(ctx, tx, `
insert into fest_players(fest_id, first_name, last_name) values(?, ?, ?)`, festID, first, last)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// registerPlayers writes the individual formats' registry: a Participant per
// player, which is what an individual game seats.
func registerPlayers(ctx context.Context, tx *sql.Tx, festID int64, names []string) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	for i, name := range names {
		id, err := store.InsertReturningID(ctx, tx, `
insert into participants(fest_id, roster, name, city, number) values(?, 'player', ?, '', ?)`,
			festID, name, i+1)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// mark deals one cell of an answer grid for `entrant` — a Participant id,
// which is unique in the fest. Coprime strides give a pattern with no visible
// period down a column or across a row, and the entrant moves the threshold as
// well as the phase, so two entrants play a sheet of different SHAPE and a
// different SCORE. That second half matters: entrants tied on every metric are
// separated by a random draw (domain/structure), and a fixture whose reseed is
// decided by a coin toss cannot have goldens. checkNoTies holds the line.
func mark(entrant, a, b, c int) string {
	// The threshold RISES with the entrant over a wide modulus, so a stronger
	// entrant takes strictly more of a sheet than a weaker one and two of them
	// cannot post the same score; the phase moves too, so the sheets do not
	// merely nest.
	skill := markFloor + entrant
	switch n := (a*31 + b*17 + c*7 + entrant*13) % markSpan; {
	case n < skill:
		return "right"
	case n < skill+markWrong:
		return "wrong"
	default:
		return ""
	}
}

// The mark pattern's dial. markSpan is wide enough that markFloor+entrant
// still leaves room above for the fest's whole registry.
const (
	markSpan  = 53
	markFloor = 14
	markWrong = 4
)
