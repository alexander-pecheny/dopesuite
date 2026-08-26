package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"dope/dope/domain/flatgame"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/storage/store"
)

// assortiFixture is what read-assorti-sheets.py carried out of the workbook.
type assortiFixture struct {
	Spec         string                 `json:"spec"`
	Participants []games.KSIParticipant `json:"participants"`
	Declined     []string               `json:"declined"`
	Games        [][][]int              `json:"games"`
}

func readAssorti(path string) (assortiFixture, error) {
	var fixture assortiFixture
	raw, err := os.ReadFile(path)
	if err != nil {
		return fixture, err
	}
	return fixture, json.Unmarshal(raw, &fixture)
}

// buildAssorti creates the Мультиигры game and writes its document the way the
// page writes it — one whole-document save, since a flat game's Protocol keeps
// everything on its one бой.
func buildAssorti(ctx context.Context, db *sql.DB, festID int64, registry map[string]int64,
	fixture assortiFixture) error {
	minigames, err := games.ParseMultiGames(fixture.Spec)
	if err != nil {
		return err
	}
	entrants := make([]int64, 0, len(fixture.Participants))
	for _, team := range fixture.Participants {
		entrants = append(entrants, registry[team.Name])
	}
	gameID, err := createGame(ctx, db, gamebuild.Spec{
		FestID:    festID,
		Type:      games.Multi,
		Label:     "Ассорти",
		Minigames: minigames,
		Entrants:  entrants,
	})
	if err != nil {
		return err
	}
	// The Protocol names a Мультиигры game after its format; this фест called
	// its own «Ассорти».
	if _, err := db.Exec(`update games set title = ? where id = ?`, "Ассорти", gameID); err != nil {
		return err
	}
	log.Printf("ассорти: игра %d, мини-игр %d, команд %d", gameID, len(minigames), len(fixture.Participants))

	// A flat game's team list IS its document — that is what seats its one бой —
	// so it is written from the workbook's own order, carrying each team's фест
	// number so the registry and the sheet name the same team.
	numbers, err := festNumbers(db, festID)
	if err != nil {
		return err
	}
	declined := map[string]bool{}
	for _, name := range fixture.Declined {
		declined[name] = true
	}
	marks := map[string]bool{}
	rows := make([][][]int, len(fixture.Games))
	participants := make([]games.KSIParticipant, len(fixture.Participants))
	for i, team := range fixture.Participants {
		participants[i] = games.KSIParticipant{Number: numbers[team.Name], Name: team.Name}
		if declined[team.Name] {
			marks[games.KSIDeclinedKey(numbers[team.Name], team.Name)] = true
		}
	}
	for g := range rows {
		width := len(minigames[g].Columns)
		rows[g] = make([][]int, len(participants))
		for i := range participants {
			row := make([]int, width)
			if i < len(fixture.Games[g]) {
				copy(row, fixture.Games[g][i])
			}
			rows[g][i] = row
		}
	}
	grids := make([]map[string]any, len(rows))
	for g := range rows {
		grids[g] = map[string]any{"cells": rows[g]}
	}
	document, err := json.Marshal(map[string]any{
		"participants": participants,
		"declined":     marks,
		"games":        grids,
		"finished":     true,
	})
	if err != nil {
		return err
	}

	var matchID sql.NullInt64
	if id, err := store.FlatMatchID(ctx, db, gameID); err == nil {
		matchID = sql.NullInt64{Int64: id, Valid: true}
	} else {
		return fmt.Errorf("бой мультиигр: %w", err)
	}
	return inTx(db, func(tx *sql.Tx) error {
		return flatgame.SaveDocumentTx(ctx, tx, festID, gameID, matchID, string(document), nil)
	})
}

// festNumbers is the фест registry by name — the number every game of the фест
// calls a team by.
func festNumbers(db *sql.DB, festID int64) (map[string]int, error) {
	rows, err := db.Query(`select name, coalesce(number, 0) from participants where fest_id = ?`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var number int
		if err := rows.Scan(&name, &number); err != nil {
			return nil, err
		}
		out[name] = number
	}
	return out, rows.Err()
}
