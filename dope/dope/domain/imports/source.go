package imports

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"strings"

	"dope/dope/domain/games"
	rosterpkg "dope/dope/domain/roster"
	"dope/dope/domain/schemedsl"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// Where a Game's Entrant list comes from (CONTEXT.md, Entrant list), read in
// one place: what the format seats (Kind), what the scheme's [init] declares
// (Declared), where the list comes from (SourceFor), and who it holds when
// nobody chose anyone (DefaultEntrants). gamebuild, entrants and the server
// ask this file rather than reading the scheme or the source words
// themselves.

// What an entrant is in a format: a team, a troika or a player.
const (
	KindTeam   = "team"
	KindTroika = "troika"
	KindPlayer = "player"
)

// KindOf is what a format seats.
func KindOf(gameType string) string {
	switch {
	case games.SeatsTroikas(gameType):
		return KindTroika
	case games.IsIndividual(gameType):
		return KindPlayer
	}
	return KindTeam
}

// ParticipantRoster is the participants.roster value of the Participants a
// kind seats. A troika is an assembled team.
func ParticipantRoster(kind string) string {
	if kind == KindPlayer {
		return "player"
	}
	return "team"
}

// The source kinds. A list stores the word, or a Game's code for SourceGame.
// SourceKSI is the legacy word for the fest's first KSI, which the seed tab's
// old button stored.
const (
	SourceGame    = "game"
	SourceFest    = "fest"
	SourceTroikas = "troikas"
	SourceRandom  = "random"
	SourceXLSX    = "xlsx"
	SourcePlayers = "players"
	SourceKSI     = seedSourceKSI
)

// Source is where a list comes from: a Game's table (Game is its code), the
// fest's own roster, the fest's troikas, a lot, an uploaded sheet, the
// by-players seeding a scheme declares, or the fest's first KSI. Division
// keeps it to one division.
type Source struct {
	// Fresh takes the source's list alone, dropping the host's hand edits
	// that an import otherwise applies again (ADR-0025).
	Fresh    bool   `json:"fresh,omitempty"`
	Kind     string `json:"kind"`
	Game     string `json:"game,omitempty"`
	Division string `json:"division,omitempty"`
}

// ParseSource reads a source as a list or a scheme stores it: a word, or else
// a Game's code.
func ParseSource(stored, division string) Source {
	stored = strings.TrimSpace(stored)
	switch stored {
	case "":
		return Source{}
	case SourceFest, SourceTroikas, SourceRandom, SourceXLSX, SourcePlayers:
		return Source{Kind: stored, Division: division}
	case SourceKSI:
		return Source{Kind: SourceKSI}
	}
	return Source{Kind: SourceGame, Game: stored, Division: division}
}

// Seeder is the SeedSource that reads the source. file is the uploaded sheet
// of an xlsx source.
func (s Source) Seeder(file io.Reader) (SeedSource, error) {
	switch s.Kind {
	case SourceGame:
		if strings.TrimSpace(s.Game) == "" {
			return nil, corei18n.User(dopestrings.Default.Entrants.Error.SourceMissing())
		}
		return FromGame(s.Game, s.Division), nil
	case SourceFest:
		return FromFest(s.Division), nil
	case SourceTroikas:
		return FromTroikas(s.Division), nil
	case SourceRandom:
		return FromRandom(), nil
	case SourcePlayers:
		return FromDeclaredPlayers(), nil
	case SourceKSI:
		return FromKSI(), nil
	case SourceXLSX:
		if file == nil {
			return nil, corei18n.User(dopestrings.Default.Server.SeedImport.FileMissing())
		}
		return FromXLSX(file), nil
	}
	return nil, corei18n.User(dopestrings.Default.Entrants.Error.SourceMissing())
}

// Declared is what a Game's [init] says about who it seats, read from the
// compiled scheme (schemedsl.ReadInit wrote it there).
type Declared struct {
	// Seed is the seed source [init] names: random, xlsx, players or a
	// Game's code; "" when it names none.
	Seed    string
	Sort    []store.SchemeSortRule
	Players *store.SchemePlayerSeed
	// Division keeps a declared seed to one division. Without a seed it
	// names the division whose troikas a Troika Game takes.
	Division string
	// DSL says the Game is built from a scheme DSL. A pasted scheme is not,
	// and never follows its list.
	DSL bool
}

// DeclaredOf reads what a compiled scheme declares.
func DeclaredOf(scheme store.FestScheme, hasDSL bool) Declared {
	return declaredOf(schemedsl.Init{Seeding: scheme.Seeding, Division: scheme.Division}, hasDSL)
}

// DeclaredOfInit reads what a parsed [init] declares, for a Game that is
// not compiled yet.
func DeclaredOfInit(init schemedsl.Init) Declared {
	return declaredOf(init, true)
}

func declaredOf(init schemedsl.Init, hasDSL bool) Declared {
	d := Declared{DSL: hasDSL}
	if seeding := init.Seeding; seeding != nil && strings.TrimSpace(seeding.Source) != "" {
		d.Seed = strings.TrimSpace(seeding.Source)
		d.Sort, d.Players, d.Division = seeding.Sort, seeding.Players, seeding.Division
		return d
	}
	if hasDSL {
		d.Division = strings.TrimSpace(init.Division)
	}
	return d
}

// LoadDeclared reads what a Game's [init] declares.
func LoadDeclared(ctx context.Context, q store.Queryer, gameID int64) (Declared, error) {
	var schemeJSON, dsl string
	if err := q.QueryRowContext(ctx, `
select coalesce(scheme_json, '{}'), coalesce(scheme_dsl, '') from games where id = ?`, gameID).Scan(&schemeJSON, &dsl); err != nil {
		return Declared{}, err
	}
	return DeclaredOfJSON(schemeJSON, dsl)
}

// DeclaredOfJSON reads what a stored scheme declares; dsl is the Game's
// scheme DSL, "" for a pasted scheme.
func DeclaredOfJSON(schemeJSON, dsl string) (Declared, error) {
	var scheme store.FestScheme
	if strings.TrimSpace(schemeJSON) != "" {
		if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
			return Declared{}, err
		}
	}
	return DeclaredOf(scheme, strings.TrimSpace(dsl) != ""), nil
}

// Seeded says [init] names a seed source, which then decides the entrants
// instead of the form.
func (d Declared) Seeded() bool { return d.Seed != "" }

// EntrantSized says the Game's Structure is compiled against its entrants and
// follows the list: a DSL with no seed. A Game seeded from a source has a
// Structure of fixed size, and the list fills its seats.
func (d Declared) EntrantSized() bool { return d.DSL && !d.Seeded() }

// EntrantDivision is the division a Troika Game takes its troikas from:
// division in [init] with no seed (entrants/festroster.go follows it).
func (d Declared) EntrantDivision() (string, bool) {
	if d.Seeded() || d.Division == "" {
		return "", false
	}
	return d.Division, true
}

// Source is the declared seed as a list source, kept to its division.
func (d Declared) Source() Source {
	return ParseSource(d.Seed, d.Division)
}

// DefaultSource is where a format's list comes from when nothing else says:
// the troikas for a format that seats them (those of the declared division,
// if any), else the fest's own roster.
func DefaultSource(kind string, declared Declared) Source {
	if kind == KindTroika {
		division, _ := declared.EntrantDivision()
		return Source{Kind: SourceTroikas, Division: division}
	}
	return Source{Kind: SourceFest}
}

// SourceFor is where a Game's list comes from: what it was last imported
// from (stored and its division), else the seed its [init] declares, else the
// format's default.
func SourceFor(kind string, stored Source, declared Declared) Source {
	if stored.Kind != "" {
		return stored
	}
	if declared.Seeded() {
		return declared.Source()
	}
	return DefaultSource(kind, declared)
}

// DefaultEntrant is one entrant of the default list. Number is a team's fest
// number (0: none) or a player's place in the registration order; a troika
// has its Participant, a player its fest_players row.
type DefaultEntrant struct {
	Number        int64
	Name, City    string
	ParticipantID int64
	FestPlayerID  int64
}

// DefaultEntrants is who a Game of the kind seats when nobody chose anyone:
// the fest's teams by number, its troikas in the order of applications (those
// of one division when one is given, less the one excluded, a troika about to
// be deleted), or its players in the order they registered. A Troika never
// seats the fest's teams.
func DefaultEntrants(ctx context.Context, q store.Queryer, festID int64, kind, division string, exclude int64) ([]DefaultEntrant, error) {
	switch kind {
	case KindTroika:
		var troikas []rosterpkg.Assembled
		var err error
		if strings.TrimSpace(division) != "" {
			troikas, err = rosterpkg.AssembledInDivision(ctx, q, festID, division, exclude)
		} else {
			troikas, err = rosterpkg.LoadAssembled(ctx, q, festID)
		}
		if err != nil {
			return nil, err
		}
		out := make([]DefaultEntrant, 0, len(troikas))
		for _, t := range troikas {
			if t.ID != exclude {
				out = append(out, DefaultEntrant{Name: t.Name, ParticipantID: t.ID})
			}
		}
		return out, nil
	case KindPlayer:
		return store.CollectRows(ctx, q, `
select id, trim(first_name || ' ' || last_name) from fest_players where fest_id = ? order by id`, []any{festID},
			func(rows *sql.Rows) (DefaultEntrant, error) {
				var e DefaultEntrant
				return e, rows.Scan(&e.FestPlayerID, &e.Name)
			})
	}
	return store.CollectRows(ctx, q, `
select coalesce(number, 0), name, coalesce(city, '') from fest_teams
where fest_id = ? and deleted = 0 order by coalesce(number, 0), position, id`, []any{festID},
		func(rows *sql.Rows) (DefaultEntrant, error) {
			var e DefaultEntrant
			return e, rows.Scan(&e.Number, &e.Name, &e.City)
		})
}
