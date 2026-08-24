// Command octobearfest builds the VIII Octobearfest фест into an existing dope
// database: the Троечка replayed from its transcript, and the «Ассорти»
// мультиигры loaded from its fixture. It adds one фест and touches nothing
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
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"

	"dope/dope/domain/gamebuild"
	"dope/dope/domain/replay"
	"dope/dope/platform/util"
)

func main() {
	dbPath := flag.String("db", "", "путь к базе dope")
	slug := flag.String("slug", "octobearfest2025", "slug феста")
	root := flag.String("root", ".", "корень модуля: откуда читать стенограмму, схему и набор")
	flag.Parse()
	if *dbPath == "" {
		log.Fatal("укажите -db")
	}
	if err := run(*dbPath, *slug, *root); err != nil {
		log.Fatal(err)
	}
}

func run(dbPath, slug, root string) error {
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
		return fmt.Errorf("фест %s в этой базе уже есть — удалите его или возьмите другой slug", slug)
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
	log.Printf("фест %d (%s)", festID, slug)

	// One registry for the фест: everyone who played either game, Троечка's
	// forty-eight first so their numbers are the посев the schema deals by.
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
	log.Printf("реестр: %d команд", len(registry))

	if err := buildTroika(ctx, db, festID, registry, script, root); err != nil {
		return fmt.Errorf("троечка: %w", err)
	}
	if err := buildAssorti(ctx, db, festID, registry, assorti); err != nil {
		return fmt.Errorf("ассорти: %w", err)
	}

	// Whoever organises anything on this instance organises this too, so the
	// фест is reachable from the host tree rather than only by its public URL.
	if _, err := db.Exec(`
insert or ignore into fest_organizers(fest_id, user_id)
select ?, user_id from fest_organizers group by user_id`, festID); err != nil {
		log.Printf("организаторы: %v (пропущено)", err)
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
		return 0, fmt.Errorf("в базе нет системного пользователя")
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
