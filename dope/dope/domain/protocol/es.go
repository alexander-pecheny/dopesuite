package protocol

func init() { Register(es{}) }

// ESPlayersPerTheme is how many players a team seats on a theme when the
// scheme is silent — the regulations' "up to three".
const ESPlayersPerTheme = 3

// es is Erudit-Sextet: EK's match — twelve themes of five questions at 10..50,
// the same shootout, the same manual places — played by teams that send one to
// three players to each theme instead of exactly one ("from one to three
// representatives", Polifest-2025). Everything inside the match is EK's, so the
// state, the scoring and the metrics are ek's verbatim; what differs is the
// `players` param's default, the cap the page offers and the server holds to.
type es struct{ ek }

func (es) Code() string { return "es" }

// Params: EK's themes, and the seats a team fields on a theme — cascading
// through defaults, Block and Round exactly as `themes` does.
func (es) Params() []Param {
	return []Param{
		{Key: "themes", Config: "themes"},
		{Key: "players", Config: "players", Default: ESPlayersPerTheme},
	}
}
