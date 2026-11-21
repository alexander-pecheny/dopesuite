package qyserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"

	"qy/qy/domain/question"
	"qy/qy/storage/store"
)

// The preliminary sheet: nominations a Google Form collected before the
// contest opened, a rating ID and a link a row. They count for a player who
// does not nominate through the form (sheetInUse), and a question in them that
// two people nominate counts outside the slots (freeQuestions).
//
//	qy-server import-sheet <year> < sheet.json
//
// The JSON is a list of rows in the order they were sent, each
// {"rating": 25177, "at": "2026-02-02T16:36:20Z", "link": "…", "tournament": "…"}:
// the link a gotquestions question or a telegram post, a question known only by
// its number having been looked up in the gotquestions dump beforehand. A
// question that is in neither comes with its text as a nominator would paste
// it, in "text", and a link of its own making to tell it apart (the
// tournament's page with the question's number, say). The import replaces the
// year's earlier one, so it can be run again.

type sheetRow struct {
	Rating     int64  `json:"rating"`
	At         string `json:"at"`
	Link       string `json:"link"`
	Tournament string `json:"tournament"`
	Text       string `json:"text,omitempty"`
}

// maxSheet caps what import-sheet reads from stdin.
const maxSheet = 1 << 22

func runImportSheet(args []string) {
	if len(args) != 1 {
		log.Fatal("usage: qy-server import-sheet <year> < sheet.json")
	}
	year, err := strconv.Atoi(args[0])
	if err != nil {
		log.Fatal("usage: qy-server import-sheet <year> < sheet.json")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxSheet))
	if err != nil {
		log.Fatal(err)
	}
	var rows []sheetRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		log.Fatal(err)
	}
	s, err := newServer()
	if err != nil {
		log.Fatal(err)
	}
	defer s.db.Close()
	people, questions, err := s.importSheet(context.Background(), year, rows)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("import-sheet: %d nominators, %d nominations for %d", people, questions, year)
}

func (s *server) importSheet(ctx context.Context, year int, rows []sheetRow) (people, nominations int, err error) {
	c, err := store.Contest(ctx, s.db, year)
	if err != nil {
		return 0, 0, err
	}
	// The network first, outside the write lock: each link read once.
	loadedBy := map[string]store.Question{}
	var order []int64
	byRating := map[int64][]sheetRow{}
	for _, r := range rows {
		if r.Rating <= 0 || r.Link == "" {
			return 0, 0, fmt.Errorf("a row needs a rating ID and a link: %+v", r)
		}
		if _, ok := byRating[r.Rating]; !ok {
			order = append(order, r.Rating)
		}
		byRating[r.Rating] = append(byRating[r.Rating], r)
		if _, ok := loadedBy[r.Link]; !ok {
			loadedBy[r.Link] = s.sheetQuestion(ctx, year, r)
		}
	}
	names := s.buff.Players(ctx, order)
	err = s.withWriteTx(ctx, "import-sheet", func(ctx context.Context, tx *sql.Tx) error {
		var old []int64
		olds, err := tx.QueryContext(ctx, `select question_id from nominations where year = ? and sheet = 1`, year)
		if err != nil {
			return err
		}
		for olds.Next() {
			var id int64
			if err := olds.Scan(&id); err != nil {
				olds.Close()
				return err
			}
			old = append(old, id)
		}
		olds.Close()
		if _, err := tx.ExecContext(ctx, `delete from nominations where year = ? and sheet = 1`, year); err != nil {
			return err
		}
		for _, rating := range order {
			user, err := s.sheetUser(ctx, tx, rating, names[rating])
			if err != nil {
				return err
			}
			seen := map[int64]bool{}
			slot := 0
			for _, r := range byRating[rating] {
				id, err := s.storeSheetQuestion(ctx, tx, loadedBy[r.Link])
				if err != nil {
					return err
				}
				if seen[id] {
					continue
				}
				seen[id] = true
				slot++
				if slot > c.MainSlots+c.ReserveSlots {
					return fmt.Errorf("rating ID %d has more nominations than %d slots", rating, c.MainSlots+c.ReserveSlots)
				}
				if _, err := tx.ExecContext(ctx, `insert into nominations(year, user_id, slot, question_id, created_at, sheet) values(?, ?, ?, ?, ?, 1)`,
					year, user, slot, id, r.At); err != nil {
					return err
				}
				nominations++
			}
			people++
		}
		// A question only the earlier import nominated, and nobody has
		// touched since, goes with it.
		for _, id := range old {
			if _, err := tx.ExecContext(ctx, `
delete from questions where id = ? and qy is null and status = 'new'
  and not exists (select 1 from nominations where question_id = ?)`, id, id); err != nil {
				return err
			}
		}
		return nil
	})
	return people, nominations, err
}

// sheetQuestion is a row's question as nominated: parsed from the text the
// row brings, read by its link, or, if the link does not load, waiting for an
// admin to paste its text.
func (s *server) sheetQuestion(ctx context.Context, year int, r sheetRow) store.Question {
	links := question.FindLinks(r.Link)
	tournament := question.FindLinks(r.Tournament).TournamentID
	q := store.Question{
		Year: int64(year), SourceText: r.Link, Link: r.Link, Published: true,
		GQID: links.GotQuestionsID, TournamentID: tournament, TournamentLink: tournamentLink(tournament),
	}
	if q.GQID != 0 {
		q.Link = links.GotQuestions
	}
	if r.Text != "" {
		q.SourceText = r.Text
		if parsed, ok := question.Parse(r.Text); ok {
			q.Orig, q.Clean = parsed, parsed.Anonymous()
			return q
		}
		log.Printf("import-sheet: the text for %s did not parse; an admin pastes it", r.Link)
		return q
	}
	l, err := s.loadQuestion(ctx, r.Link)
	if err != nil {
		log.Printf("import-sheet: %s did not load (%v); an admin pastes it", r.Link, err)
		return q
	}
	q.Orig = l.q
	q.Orig.Text = question.WithHandout(l.q.Text, l.handout)
	q.Clean = q.Orig.Anonymous()
	if q.TournamentLink == "" {
		q.TournamentLink = l.tournament
		q.TournamentID = question.FindLinks(l.tournament).TournamentID
	}
	if q.GQID != 0 {
		if found, err := s.gq.Question(ctx, q.GQID); err == nil {
			q.TournamentTitle = found.Tournament
		}
	}
	return q
}

// storeSheetQuestion is the year's question at the link, made if new: one
// gotquestions question or one telegram post is one question.
func (s *server) storeSheetQuestion(ctx context.Context, tx *sql.Tx, q store.Question) (int64, error) {
	if q.GQID == 0 {
		var id int64
		err := tx.QueryRowContext(ctx, `select id from questions where year = ? and link = ?`, q.Year, q.Link).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	return s.storeQuestion(ctx, tx, q)
}

// sheetUser is the nominator the sheet names by rating ID: a user nobody logs
// in as, kept apart from the player's own account, if they have one, so that
// the sheet never shows up in their form.
func (s *server) sheetUser(ctx context.Context, tx *sql.Tx, rating int64, name string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
select id from users where rating_id = ? and telegram_user_id is null and telegram_username is null and username is null`, rating).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `insert into users(rating_id, telegram_name, created_at, updated_at) values(?, ?, ?, ?)`,
		rating, sql.NullString{String: name, Valid: name != ""}, s.stamp(), s.stamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
