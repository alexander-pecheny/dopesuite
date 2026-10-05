package gamebuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// FillBracketsTx writes a double elimination's brackets into a Game's stored
// scheme: the bracket each bout is played in (store.SchemeMatch.Bracket), as
// its DSL compiles today. A scheme compiled before bouts carried one has
// none, and the fest grid colours the two brackets by it. Nothing else of the
// scheme changes, and no bout, seat or result is touched; a Game whose DSL
// has no double elimination, or no longer compiles, is left as it is.
func FillBracketsTx(ctx context.Context, tx *sql.Tx, gameID int64) error {
	var festID int64
	var gameType, stored, dsl string
	if err := tx.QueryRowContext(ctx, `
select fest_id, game_type, coalesce(scheme_json, ''), coalesce(scheme_dsl, '') from games where id = ?`,
		gameID).Scan(&festID, &gameType, &stored, &dsl); err != nil {
		return err
	}
	if !strings.Contains(dsl, "double_elimination") || stored == "" {
		return nil
	}
	var meta struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	_ = json.Unmarshal([]byte(stored), &meta)
	entrants, err := gameEntrantsTx(ctx, tx, gameID)
	if err != nil {
		return err
	}
	scheme, err := schemeForEntrantsTx(ctx, tx, festID, gameType, meta.Slug, meta.Title, dsl, entrants)
	if err != nil {
		return nil
	}
	brackets := map[string]string{}
	for _, stage := range scheme.Stages {
		for _, match := range stage.Matches {
			if match.Bracket != "" {
				brackets[match.Code] = match.Bracket
			}
		}
	}
	if len(brackets) == 0 {
		return nil
	}
	// The stored scheme is patched as JSON, not re-marshalled from the
	// struct, so a field this build does not know survives.
	var doc map[string]any
	if err := json.Unmarshal([]byte(stored), &doc); err != nil {
		return nil
	}
	stages, _ := doc["stages"].([]any)
	for _, stage := range stages {
		matches, _ := stage.(map[string]any)["matches"].([]any)
		for _, match := range matches {
			fields, _ := match.(map[string]any)
			if bracket := brackets[str(fields["code"])]; bracket != "" && fields != nil {
				fields["bracket"] = bracket
			}
		}
	}
	patched, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update games set scheme_json = ? where id = ?`, string(patched), gameID)
	return err
}

func str(value any) string {
	text, _ := value.(string)
	return text
}
