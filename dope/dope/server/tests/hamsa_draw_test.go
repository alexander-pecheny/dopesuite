package tests

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/storage/store"
)

// hamsaFest builds a Хамса game of twelve teams on the tournament's own
// scheme, seeded in roster order.
func hamsaFest(t *testing.T) (*serverGame, []string, map[string]int64) {
	t.Helper()
	srv := newAuthTestServer(t)
	// One edit at a time, each awaited: these tests read the fest view back
	// through the API after every write, so they go the way a host does.
	srv.SetEditBatchWindow(time.Millisecond)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "hamsa", "Город героев", systemUserID(t, db))

	var names []string
	numbers := map[string]int{}
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("Команда %d", i)
		names = append(names, name)
		numbers[name] = i
	}
	teams := registerNumberedTeams(t, db, festID, numbers)
	// The письменный отбор is a КСИ Game of its own, and the scheme seeds from
	// it: without it there is nothing for `seed: ksi-1` to name.
	createKSIGame(t, db, festID)
	gameID := createSchemeGameFor(t, db, festID, games.Hamsa, "Хамса",
		readFile(t, "../../../scripts/hamsa/hamsa.dsl"), idsFor(t, teams, names))
	game := &serverGame{t: t, srv: srv, festID: festID, gameID: gameID, gameType: games.Hamsa, token: token}
	game.via = httpTransport{game}
	return game, names, teams
}

func createKSIGame(t *testing.T, db *sql.DB, festID int64) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := gamebuild.Create(t.Context(), tx, gamebuild.Spec{
		FestID: festID, Type: games.KSI, Label: "КСИ", KSIThemes: 10,
	}); err != nil {
		t.Fatalf("создать КСИ: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// drawSlotsOf reads every Draw Slot the fest view carries for a stage, with
// the candidates the server resolved for it.
func drawSlotsOf(t *testing.T, game *serverGame, stageCode string) []store.DrawSlotView {
	t.Helper()
	resp := scopedAPIRequest(t, game.srv, http.MethodGet,
		fmt.Sprintf("/api/fest/%d/games/%d", game.festID, game.gameID), nil, game.token)
	if resp.Code != http.StatusOK {
		t.Fatalf("фест: %d %s", resp.Code, resp.Body.String())
	}
	var view struct {
		Stages []struct {
			Code    string `json:"code"`
			Matches []struct {
				Participants []store.MatchParticipantSummary `json:"participants"`
			} `json:"matches"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	var out []store.DrawSlotView
	for _, stage := range view.Stages {
		if stage.Code != stageCode {
			continue
		}
		for _, match := range stage.Matches {
			for _, seat := range match.Participants {
				if seat.Draw != nil {
					out = append(out, *seat.Draw)
				}
			}
		}
	}
	return out
}

func playHamsaBlockRound(t *testing.T, game *serverGame, blockRound int) {
	t.Helper()
	rows, err := game.db().Query(`
select m.id, m.code from matches m join stages s on s.id = m.stage_id
where m.game_id = ? and m.round = ? and s.code like 's1-%' order by m.position`, game.gameID, blockRound)
	if err != nil {
		t.Fatal(err)
	}
	type bout struct {
		id   int64
		code string
	}
	var bouts []bout
	for rows.Next() {
		var b bout
		if err := rows.Scan(&b.id, &b.code); err != nil {
			t.Fatal(err)
		}
		bouts = append(bouts, b)
	}
	rows.Close()
	for _, b := range bouts {
		seats, err := store.CollectRows(t.Context(), game.db(), `
select participant_id from match_slots where match_id = ? and participant_id is not null order by slot_index`,
			[]any{b.id}, func(rows *sql.Rows) (int64, error) {
				var id int64
				return id, rows.Scan(&id)
			})
		if err != nil {
			t.Fatal(err)
		}
		if len(seats) != 4 {
			t.Fatalf("бой %s сел на %d мест", b.code, len(seats))
		}
		// The higher a team sits at the table, the more it takes: four
		// distinct scores, so the бой places everyone.
		var ops []map[string]any
		for seat, id := range seats {
			for q := 0; q <= 3-seat; q++ {
				ops = append(ops, map[string]any{
					"path":  []any{"participants", fmt.Sprint(id), "themes", 0, "answers", q},
					"value": "right",
				})
			}
		}
		if err := game.via.patch(b.id, b.code, ops); err != nil {
			t.Fatalf("отметки %s: %v", b.code, err)
		}
		if err := game.via.finish(b.id, b.code); err != nil {
			t.Fatalf("закрытие %s: %v", b.code, err)
		}
	}
}

// Игра №2 seats nine teams by their places and leaves three seats to the
// blind draw: until a host runs it, those seats are empty and the panel
// offers exactly the three fourth-place teams.
func TestHamsaDrawSeatsTheFourthPlaces(t *testing.T) {
	game, _, _ := hamsaFest(t)

	if slots := drawSlotsOf(t, game, "s1-r2"); len(slots) != 3 {
		t.Fatalf("жеребьёвочных мест %d, want 3", len(slots))
	} else {
		for _, slot := range slots {
			if len(slot.Candidates) != 0 {
				t.Fatalf("%s предлагает кандидатов до первой игры: %+v", slot.Code, slot.Candidates)
			}
		}
	}

	playHamsaBlockRound(t, game, 1)

	slots := drawSlotsOf(t, game, "s1-r2")
	if len(slots) != 3 {
		t.Fatalf("жеребьёвочных мест %d", len(slots))
	}
	fourth := slots[0].Candidates
	if len(fourth) != 3 {
		t.Fatalf("кандидатов %d, want три четвёртых места: %+v", len(fourth), fourth)
	}
	for _, slot := range slots {
		if len(slot.Candidates) != 3 || slot.Seated != 0 {
			t.Fatalf("%s = %+v", slot.Code, slot)
		}
	}

	// The host draws each fourth place into a table of its own.
	for i, slot := range slots {
		resp := scopedAPIRequest(t, game.srv, http.MethodPut,
			fmt.Sprintf("/api/fest/%d/games/%d/draw", game.festID, game.gameID),
			map[string]any{"slot": slot.Code, "participant": fourth[i].ID}, game.token)
		if resp.Code != http.StatusOK {
			t.Fatalf("жеребьёвка %s: %d %s", slot.Code, resp.Code, resp.Body.String())
		}
	}
	for i, slot := range drawSlotsOf(t, game, "s1-r2") {
		if slot.Seated != fourth[i].ID {
			t.Fatalf("%s посадил %d, want %d", slot.Code, slot.Seated, fourth[i].ID)
		}
	}

	// A team the slot does not name, and one already drawn, are both refused.
	first := drawSlotsOf(t, game, "s1-r2")[0]
	resp := scopedAPIRequest(t, game.srv, http.MethodPut,
		fmt.Sprintf("/api/fest/%d/games/%d/draw", game.festID, game.gameID),
		map[string]any{"slot": first.Code, "participant": fourth[1].ID}, game.token)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("дважды посаженная команда: %d %s", resp.Code, resp.Body.String())
	}
	resp = scopedAPIRequest(t, game.srv, http.MethodPut,
		fmt.Sprintf("/api/fest/%d/games/%d/draw", game.festID, game.gameID),
		map[string]any{"slot": "s1-r2-m1-d9", "participant": fourth[0].ID}, game.token)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("несуществующее место: %d %s", resp.Code, resp.Body.String())
	}
}

// The Block is one ranking scope: its table is the сумма мест over both Игры,
// and it is what the Финал seats from.
func TestHamsaBlockTableRanksBothBlockRounds(t *testing.T) {
	game, names, teams := hamsaFest(t)
	playHamsaBlockRound(t, game, 1)
	slots := drawSlotsOf(t, game, "s1-r2")
	fourth := slots[0].Candidates
	for i, slot := range slots {
		resp := scopedAPIRequest(t, game.srv, http.MethodPut,
			fmt.Sprintf("/api/fest/%d/games/%d/draw", game.festID, game.gameID),
			map[string]any{"slot": slot.Code, "participant": fourth[i].ID}, game.token)
		if resp.Code != http.StatusOK {
			t.Fatalf("жеребьёвка: %d %s", resp.Code, resp.Body.String())
		}
	}
	playHamsaBlockRound(t, game, 2)

	ranked, err := store.CollectRows(t.Context(), game.db(), `
select ss.rank, ss.participant_id, ss.metrics_json from stage_standings ss
join stages s on s.id = ss.stage_id
where s.game_id = ? and s.code = 's1-total' order by ss.rank`,
		[]any{game.gameID}, func(rows *sql.Rows) ([3]string, error) {
			var rank int
			var id int64
			var metrics string
			err := rows.Scan(&rank, &id, &metrics)
			return [3]string{fmt.Sprint(rank), fmt.Sprint(id), metrics}, err
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 12 {
		t.Fatalf("в таблице ГЭ %d строк, want 12", len(ranked))
	}
	var top struct {
		PlaceSum float64 `json:"place_sum"`
		Bouts    float64 `json:"bouts"`
		Seed     float64 `json:"seed"`
		First    float64 `json:"first"`
	}
	if err := json.Unmarshal([]byte(ranked[0][2]), &top); err != nil {
		t.Fatal(err)
	}
	if top.Bouts != 2 {
		t.Fatalf("боёв в строке %v, want 2 — таблица считает оба раунда", top.Bouts)
	}
	if top.PlaceSum != 2 || top.First != 2 {
		t.Fatalf("первая строка = %+v, want сумму мест 2 и два первых места", top)
	}
	if top.Seed == 0 {
		t.Fatal("в таблице нет посева — по нему сортирует последний критерий регламента")
	}
	if ranked[0][1] != fmt.Sprint(teams[names[0]]) {
		t.Fatalf("ГЭ выиграл %s, want %s", ranked[0][1], names[0])
	}

	// The Финал seats the four best once the пересев is calculated.
	if err := game.via.reseeds(); err != nil {
		t.Fatalf("пересев: %v", err)
	}
	seated, err := store.CollectRows(t.Context(), game.db(), `
select coalesce(p.name, '') from match_slots ms
join matches m on m.id = ms.match_id
join stages s on s.id = m.stage_id
left join participants p on p.id = ms.participant_id
where s.game_id = ? and s.code = 's2' order by ms.slot_index`,
		[]any{game.gameID}, func(rows *sql.Rows) (string, error) {
			var name string
			return name, rows.Scan(&name)
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(seated) != 4 || seated[0] != names[0] {
		t.Fatalf("финал сел так: %v", seated)
	}
}
