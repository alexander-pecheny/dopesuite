package games

import dopestrings "dope/i18nstrings"

// esFormat is Erudit-Sextet. It rides EK's page and EK's scoring — twelve
// themes of five, the same shootout — and differs only in seating up to three
// players on a theme, which is a Protocol param, not a renderer of its own.
// Its history lists events only: the history page reads EK's rows for EK
// alone.
var esFormat = Definition{Code: ES, Label: dopestrings.Default.Games.Es.Label(), Title: dopestrings.Default.Host.Games.TypeEs(),
	Page: "static/ek.html", Init: InitEK, EKBout: true, HandRoster: true, PlayerOverrides: true,
	DSL: DSLEditable, PastedScheme: true, Sheets: SheetsEK, Journal: JournalEvents, Protocol: es{}}

// ESPlayersPerTheme is how many players a team seats on a theme when the
// scheme is silent — the regulations' "up to three".
const ESPlayersPerTheme = 3

// es is Erudit-Sextet: EK's match — twelve themes of five questions at 10..50,
// the same shootout, the same manual places — played by teams that send one to
// three players to each theme instead of exactly one ("from one to three
// representatives", Polifest-2025). Everything inside the match is EK's: es
// embeds ek and takes its TeamBlob, Started, Metrics, EmptyState, Score and
// UsedPlayers verbatim. It answers two things of its own, its Code and its
// Params, whose `players` default is the cap the page offers and the server
// holds to.
type es struct{ ek }

func (es) Code() string { return ES }

// Params: EK's themes, and the seats a team fields on a theme — cascading
// through defaults, Block and Round exactly as `themes` does.
func (es) Params() []Param {
	return []Param{
		{Key: "themes", Config: "themes"},
		{Key: "players", Config: "players", Default: ESPlayersPerTheme},
	}
}
