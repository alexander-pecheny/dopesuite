// Package buffdb reads buff's mirror of rating.chgk.info (ADR-0020). It is
// opened read-only and fails soft — a missing file, a missing table or a broken
// query gives an empty answer, never an error page, because every caller is a
// flag or a suggest.
package buffdb

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"unicode"

	_ "modernc.org/sqlite"
)

const PathEnv = "DOPE_BUFF_DB"

// Store answers questions about rating.chgk.info. The zero value is Disabled:
// every method returns nothing.
type Store struct {
	db *sql.DB
}

func Disabled() *Store { return &Store{} }

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Disabled(), nil
	}
	if _, err := os.Stat(path); err != nil {
		return Disabled(), nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return Disabled(), err
	}
	db.SetMaxOpenConns(4)
	return &Store{db: db}, nil
}

func (s *Store) Enabled() bool { return s != nil && s.db != nil }

func (s *Store) Close() error {
	if !s.Enabled() {
		return nil
	}
	return s.db.Close()
}

// TownCountries is the ISO-3166 alpha-2 code of each named town's country,
// keyed by the lowercased name the caller asked about. A town buff does not
// know, and one the rating site leaves without a country, is simply absent.
func (s *Store) TownCountries(ctx context.Context, names []string) map[string]string {
	if !s.Enabled() || len(names) == 0 {
		return nil
	}
	// SQLite's lower() folds ASCII only, so «цюрих» would never meet «Цюрих» in
	// a comparison it makes. Each name is therefore asked for as typed and as
	// the rating site spells it, and folded back in Go when the answer lands.
	args := make([]any, 0, 2*len(names))
	holes := make([]string, 0, 2*len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		for _, spelling := range []string{name, titleFold(name)} {
			args = append(args, spelling)
			holes = append(holes, "?")
		}
	}
	if len(args) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
select t.name, c.iso from towns t join countries c on c.id = t.country_id
where c.iso <> '' and t.name in (`+strings.Join(holes, ",")+`)`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, iso string
		if err := rows.Scan(&name, &iso); err != nil {
			return out
		}
		out[strings.ToLower(name)] = iso
	}
	return out
}

// titleFold is the rating site's spelling of a name: first rune upper, rest
// lower.
func titleFold(word string) string {
	runes := []rune(word)
	if len(runes) == 0 {
		return word
	}
	out := make([]rune, len(runes))
	out[0] = unicode.ToUpper(runes[0])
	for i := 1; i < len(runes); i++ {
		out[i] = unicode.ToLower(runes[i])
	}
	return string(out)
}

// CountryISO is one country's ISO-3166 alpha-2 code. The rating site publishes
// no such code, so buff writes it down when it mirrors the countries; this is
// what keeps the codes in one place rather than one per reader.
func (s *Store) CountryISO(ctx context.Context, countryID int64) string {
	if !s.Enabled() || countryID <= 0 {
		return ""
	}
	var iso string
	if err := s.db.QueryRowContext(ctx, `select iso from countries where id = ?`, countryID).Scan(&iso); err != nil {
		return ""
	}
	return iso
}

// TownCountry is the country of one town id, as buff mirrored it. The second
// result says whether buff knows the town at all, which is what tells a caller
// to go and ask the rating site — a town the site itself leaves without a
// country is known, with an empty code.
func (s *Store) TownCountry(ctx context.Context, townID int64) (string, bool) {
	if !s.Enabled() || townID <= 0 {
		return "", false
	}
	var iso sql.NullString
	err := s.db.QueryRowContext(ctx, `
select c.iso from towns t left join countries c on c.id = t.country_id
where t.id = ?`, townID).Scan(&iso)
	if err != nil {
		return "", false
	}
	return iso.String, true
}
