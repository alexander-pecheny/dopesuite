// Command octobearfest builds the VIII Octobearfest fest into an existing dope
// database: the Troika game replayed from its transcript, and the Assorti multi
// loaded from its fixture. It adds one fest and touches nothing
// else, so it can be pointed at a staging database that already holds others.
//
//	go run ./scripts/octobearfest -db /var/lib/dopetest/fest.db
//
// It is the same road a host takes — gamebuild.Create, the match patch path,
// the resolver — rather than a hand-written pile of rows, so what appears is
// what dope would have produced had the tournament been played on it.
package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"strconv"

	_ "modernc.org/sqlite"

	"dope/dope/domain/gamebuild"
	"dope/dope/domain/replay"
	"dope/dope/platform/util"
	dopestrings "dope/i18nstrings"

	core "pecheny.me/dopecore/i18nstrings"
)

func main() {
	dbPath := flag.String("db", "", dopestrings.Default.Octobearfest.Flag.Db())
	slug := flag.String("slug", "octobearfest2025", dopestrings.Default.Octobearfest.Flag.Slug())
	root := flag.String("root", ".", dopestrings.Default.Octobearfest.Flag.Root())
	flag.Parse()
	if *dbPath == "" {
		log.Fatal(dopestrings.Default.Octobearfest.Error.DbMissing())
	}
	if err := run(*dbPath, *slug, *root); err != nil {
		log.Fatal(err)
	}
}

func run(dbPath, slug, root string) error {
	s := dopestrings.Default
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("pragma foreign_keys = on"); err != nil {
		return err
	}
	ctx := context.Background()

	var exists int
	if err := db.QueryRow(`select count(*) from fests where slug = ?`, slug).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return core.User(s.Octobearfest.Error.FestExists(slug))
	}

	script, err := parseTranscript(root + "/testdata/octobearfest2025/troika.transcript")
	if err != nil {
		return err
	}
	assorti, err := readAssorti(root + "/testdata/assorti2025/assorti.json")
	if err != nil {
		return err
	}

	owner, err := systemUser(db)
	if err != nil {
		return err
	}
	festID, err := newFest(db, slug, "VIII Octobearfest", owner)
	if err != nil {
		return err
	}
	log.Printf("%s", s.Octobearfest.Log.Fest(strconv.FormatInt(festID, 10), slug))

	// One registry for the fest: everyone who played either game, Troika's
	// forty-eight first so their numbers are the seed the schema deals by.
	registry := map[string]int64{}
	numbers := map[string]int{}
	for _, team := range script.Roster {
		numbers[team.Name] = team.Number
	}
	next := len(numbers)
	for _, team := range assorti.Participants {
		if _, seen := numbers[team.Name]; !seen {
			next++
			numbers[team.Name] = next
		}
	}
	for name, number := range numbers {
		id, err := addTeam(db, festID, name, number)
		if err != nil {
			return err
		}
		registry[name] = id
	}
	log.Printf("%s", s.Octobearfest.Log.Registry(strconv.Itoa(len(registry))))

	if err := buildTroika(ctx, db, festID, registry, script, root); err != nil {
		return core.User(s.Octobearfest.Error.TroikaStep(err.Error()))
	}
	if err := buildAssorti(ctx, db, festID, registry, assorti); err != nil {
		return core.User(s.Octobearfest.Error.AssortiStep(err.Error()))
	}

	// Whoever organises anything on this instance organises this too, so the
	// fest is reachable from the host tree rather than only by its public URL.
	if _, err := db.Exec(`
insert or ignore into fest_organizers(fest_id, user_id)
select ?, user_id from fest_organizers group by user_id`, festID); err != nil {
		log.Printf("%s", s.Octobearfest.Log.Organizers(err.Error()))
	}
	return nil
}

func parseTranscript(path string) (replay.Script, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return replay.Script{}, err
	}
	return replay.Parse(string(src))
}

func systemUser(db *sql.DB) (int64, error) {
	var id int64
	err := db.QueryRow(`select id from users where is_system = 1 order by id limit 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, core.User(dopestrings.Default.Octobearfest.Error.NoSystemUser())
	}
	return id, err
}

func newFest(db *sql.DB, slug, title string, owner int64) (int64, error) {
	now := util.UtcNow()
	res, err := db.Exec(`
insert into fests(slug, title, revision, created_at, updated_at, created_by, is_public)
values(?, ?, 1, ?, ?, ?, 1)`, slug, title, now, now, owner)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func addTeam(db *sql.DB, festID int64, name string, number int) (int64, error) {
	res, err := db.Exec(`
insert into participants(fest_id, roster, name, city, number) values(?, 'team', ?, '', ?)`,
		festID, name, number)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func createGame(ctx context.Context, db *sql.DB, spec gamebuild.Spec) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := gamebuild.Create(ctx, tx, spec)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
