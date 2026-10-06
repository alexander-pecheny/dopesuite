// Package festops holds the host's writes to a fest's Games: create, settings,
// clear and delete, and the delete of the fest itself. Each is the body of
// one transaction and says what it did (core.FestWrite). The host form and its
// JSON twin (ADR-0021) both run it through core.Engine.CommitFestWrite, so they
// share the rules and the write discipline: the connection before the lock,
// one revision, the engine's pointers settled after the commit.
package festops

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/entrants"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/roster"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/journal"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// CreateGameTx creates a Game from its spec and anchors its journal: the
// genesis checkpoint is what a later per-game revert replays from.
func CreateGameTx(ctx context.Context, tx *sql.Tx, spec gamebuild.Spec) (int64, core.FestWrite, error) {
	if err := festExistsTx(ctx, tx, spec.FestID); err != nil {
		return 0, core.FestWrite{}, err
	}
	gameID, err := gamebuild.Create(ctx, tx, spec)
	if err != nil {
		return 0, core.FestWrite{}, err
	}
	revision, err := bumpTx(ctx, tx, spec.FestID, "game:create", map[string]any{"gameID": gameID, "gameType": spec.Type})
	if err != nil {
		return 0, core.FestWrite{}, err
	}
	// The checkpoint is taken after the revision, so it sits at or before any
	// later edit of the Game.
	if err := journal.WriteGameCheckpoint(ctx, tx, gameID, core.JournalIDForSeqTx(ctx, tx)); err != nil {
		return 0, core.FestWrite{}, err
	}
	return gameID, core.FestWrite{Revision: revision}, nil
}

// Settings is what a Game's settings page edits: its title, its slug, its
// scheme, and the Divisions it hides. An empty SchemeDSL leaves the scheme as
// it is.
type Settings struct {
	Title     string `json:"title"`
	Slug      string `json:"slug"`
	SchemeDSL string `json:"scheme_dsl"`
	// HiddenDivisions are the Flags whose Divisions the Game does not show,
	// written as given; nil leaves them as they are.
	HiddenDivisions *[]string `json:"hidden_divisions"`
	// ShownDivisions are the Divisions the Game offers that it shows, as the
	// settings page ticks them: every other one it offers is hidden, and one
	// hidden before that it no longer offers stays hidden. When it is set,
	// HiddenDivisions is not read.
	ShownDivisions *[]string `json:"shown_divisions"`
}

// storedSettings is what UpdateSettingsTx checks Settings against.
type storedSettings struct {
	gameType  string
	schemeDSL string
	hidden    []string
}

// UpdateSettingsTx saves a Game's settings. A changed scheme recompiles the
// Game in the same transaction, so a refused recompile leaves nothing
// half-applied, and a Troika game that now takes a division, or another one,
// seats its troikas (entrants.RecompileTx); the write names the Troika Games
// for the broadcast. Only a format whose scheme its settings page edits
// (games.DSLEditable) takes a changed scheme; a flat Game is shaped by its own
// fields, even one created from a DSL.
func UpdateSettingsTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, g Settings) (core.FestWrite, error) {
	s := dopestrings.Default
	title := strings.TrimSpace(g.Title)
	if title == "" {
		return core.FestWrite{}, corei18n.User(s.Host.Games.ErrorTitleRequired())
	}
	stored, err := loadStoredSettingsTx(ctx, tx, festID, gameID)
	if err != nil {
		return core.FestWrite{}, err
	}
	schemeChanged := strings.TrimSpace(g.SchemeDSL) != "" && strings.TrimSpace(g.SchemeDSL) != strings.TrimSpace(stored.schemeDSL)
	if schemeChanged && games.Get(stored.gameType).DSL != games.DSLEditable {
		return core.FestWrite{}, corei18n.User(s.Host.Games.ErrorSchemeNotEditable(games.Label(stored.gameType)))
	}
	slug, err := freeSlugTx(ctx, tx, festID, gameID, g.Slug)
	if err != nil {
		return core.FestWrite{}, err
	}
	if _, err := tx.ExecContext(ctx, `
update games set title = ?, slug = ?, updated_at = ? where id = ? and fest_id = ?`,
		title, slug, util.UtcNow(), gameID, festID); err != nil {
		return core.FestWrite{}, err
	}
	if err := writeHiddenDivisionsTx(ctx, tx, festID, gameID, g, stored); err != nil {
		return core.FestWrite{}, err
	}
	written := core.FestWrite{Event: "game:settings", Payload: map[string]any{"gameID": gameID}}
	if !schemeChanged {
		return written, nil
	}
	if written.Broadcast.Views, err = entrants.RecompileTx(ctx, tx, festID, gameID, g.SchemeDSL); err != nil {
		return core.FestWrite{}, err
	}
	return written, nil
}

// loadStoredSettingsTx reads what a save compares with. A Game that is not in
// the fest is sql.ErrNoRows, which both the form and the JSON twin answer with
// a 404.
func loadStoredSettingsTx(ctx context.Context, tx *sql.Tx, festID, gameID int64) (storedSettings, error) {
	var stored storedSettings
	var hidden string
	if err := tx.QueryRowContext(ctx, `
select game_type, coalesce(scheme_dsl, ''), coalesce(hidden_divisions, '') from games where id = ? and fest_id = ?`,
		gameID, festID).Scan(&stored.gameType, &stored.schemeDSL, &hidden); err != nil {
		return storedSettings{}, err
	}
	stored.hidden = store.ParseHiddenDivisions(hidden)
	return stored, nil
}

// writeHiddenDivisionsTx saves the Divisions the Game hides, if the Settings
// say anything about them.
func writeHiddenDivisionsTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, g Settings, stored storedSettings) error {
	var hidden []string
	switch {
	case g.ShownDivisions != nil:
		offered, err := OfferedDivisions(ctx, tx, festID, stored.gameType)
		if err != nil {
			return err
		}
		hidden = HiddenAfterShown(offered, stored.hidden, *g.ShownDivisions)
	case g.HiddenDivisions != nil:
		hidden = *g.HiddenDivisions
	default:
		return nil
	}
	_, err := tx.ExecContext(ctx, `update games set hidden_divisions = ? where id = ? and fest_id = ?`,
		store.HiddenDivisionsValue(roster.CleanDivisions(hidden)), gameID, festID)
	return err
}

// HiddenAfterShown is the Divisions a Game hides once the host has ticked the
// shown ones: every offered one not ticked, and every one hidden before that is
// not offered now, so a Flag no team carries for a while stays hidden when it
// comes back.
func HiddenAfterShown(offered, hiddenBefore, shown []string) []string {
	ticked := roster.CleanDivisions(shown)
	var hidden []string
	for _, d := range offered {
		if !slices.Contains(ticked, d) {
			hidden = append(hidden, d)
		}
	}
	for _, d := range hiddenBefore {
		if !slices.Contains(offered, d) {
			hidden = append(hidden, d)
		}
	}
	return roster.CleanDivisions(hidden)
}

// OfferedDivisions is the Divisions a Game of this format offers to look at:
// the fest's (roster.FestDivisions) for a format whose results carry the chips
// (games.Definition.Divisions), and none for the rest.
func OfferedDivisions(ctx context.Context, q store.Queryer, festID int64, gameType string) ([]string, error) {
	if def, ok := games.Lookup(gameType); !ok || !def.Divisions {
		return nil, nil
	}
	return roster.FestDivisions(ctx, q, festID)
}

// freeSlugTx checks a slug the host typed: well formed, and no other Game of
// the fest using it. An empty one clears the slug (nil).
func freeSlugTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, raw string) (any, error) {
	s := dopestrings.Default
	slug := strings.TrimSpace(raw)
	if slug == "" {
		return nil, nil
	}
	if err := util.ValidateSlug(slug); err != nil {
		return nil, corei18n.User(s.Host.Games.ErrorSlugInvalid(err.Error()))
	}
	var count int
	if err := tx.QueryRowContext(ctx, `
select count(*) from games where fest_id = ? and slug = ? and id <> ?`, festID, slug, gameID).Scan(&count); err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, corei18n.User(s.Host.Games.ErrorSlugTaken())
	}
	return slug, nil
}

// ClearGameTx resets a Game to its just-created state (gamebuild.Clear),
// keeping its id, code, slug and title so its URLs stay valid. If it is the
// active Game, the active bout moves to its first.
func ClearGameTx(ctx context.Context, tx *sql.Tx, festID, gameID int64) (core.FestWrite, error) {
	firstMatchCode, err := gamebuild.Clear(ctx, tx, festID, gameID)
	if err != nil {
		return core.FestWrite{}, err
	}
	revision, err := festRevisionTx(ctx, tx, festID)
	if err != nil {
		return core.FestWrite{}, err
	}
	return core.FestWrite{Revision: revision, Settled: func(e *core.Engine, _ int64) {
		if e.FestID == festID && e.ActiveGameID == gameID {
			e.ActiveMatchCode = firstMatchCode
		}
	}}, nil
}

// DeleteGameTx deletes one Game of the fest. If it is the active Game, the
// active pointer moves to the fest's first Game left, or to none.
func DeleteGameTx(ctx context.Context, tx *sql.Tx, festID, gameID int64) (core.FestWrite, error) {
	var title string
	if err := tx.QueryRowContext(ctx, `select title from games where id = ? and fest_id = ?`, gameID, festID).Scan(&title); err != nil {
		return core.FestWrite{}, err
	}
	if _, err := tx.ExecContext(ctx, `delete from games where id = ? and fest_id = ?`, gameID, festID); err != nil {
		return core.FestWrite{}, err
	}
	var nextGameID sql.NullInt64
	var nextMatchCode sql.NullString
	if err := tx.QueryRowContext(ctx, `
select g.id, coalesce((
  select m.code from matches m where m.game_id = g.id order by m.position, m.id limit 1
), '')
from games g
where g.fest_id = ?
order by g.position, g.id
limit 1`, festID).Scan(&nextGameID, &nextMatchCode); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return core.FestWrite{}, err
	}
	return core.FestWrite{
		Event:   "game:delete",
		Payload: map[string]any{"gameID": gameID, "title": title},
		Settled: func(e *core.Engine, _ int64) {
			if e.FestID != festID || e.ActiveGameID != gameID {
				return
			}
			e.ActiveGameID, e.ActiveMatchCode = nextGameID.Int64, nextMatchCode.String
		},
	}, nil
}

// DeleteFestTx deletes a fest its creator asks to delete. sql.ErrNoRows says
// there is no such fest of theirs.
func DeleteFestTx(ctx context.Context, tx *sql.Tx, festID, creatorID int64) (core.FestWrite, error) {
	result, err := tx.ExecContext(ctx, `delete from fests where id = ? and created_by = ?`, festID, creatorID)
	if err != nil {
		return core.FestWrite{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return core.FestWrite{}, sql.ErrNoRows
	}
	return core.FestWrite{Settled: func(e *core.Engine, _ int64) {
		if e.FestID == festID {
			e.FestID, e.ActiveGameID, e.ActiveMatchCode = 0, 0, ""
		}
	}}, nil
}

// festExistsTx returns sql.ErrNoRows when there is no fest festID.
func festExistsTx(ctx context.Context, tx *sql.Tx, festID int64) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `select count(*) from fests where id = ?`, festID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func festRevisionTx(ctx context.Context, tx *sql.Tx, festID int64) (int64, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, `select revision from fests where id = ?`, festID).Scan(&revision)
	return revision, err
}

func bumpTx(ctx context.Context, tx *sql.Tx, festID int64, event string, payload any) (int64, error) {
	return festwrite.BumpFestRevisionTx(ctx, tx, festID, event, util.MustJSON(payload))
}
