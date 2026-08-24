package schemedsl

import (
	"fmt"
	"testing"

	"dope/dope/storage/store"
)

func troikaInput(entrants int) Input {
	in := Input{Slug: "troika", Title: "Тройка", GameType: "troika"}
	for i := 0; i < entrants; i++ {
		in.Entrants = append(in.Entrants, store.SchemeSlot{
			Seed: &store.SchemeSeedRef{Basket: 1, Number: i + 1},
		})
	}
	return in
}

// Троечка's финал and матч за 3-е место are seeded straight out of the 3rd
// групповой этап — победители групп в финал, вторые места в бронзу — with no
// semifinal to lose. The se block therefore takes participants: 2 and draws
// its bronze pair from the incoming Edge, consuming four.
const troikaFinalSrc = `
[defaults]
themes: 6

[scheme]
kind: roundrobin
groups: 2
group_size: 4
proceeding_participants: 2
themes: 8
---
kind: single_elimination
participants: 2
bronze: true
best_of.final: 3
best_of.bronze: 3
points: [1, 0.5, 0]
standings.rating: points + taken / 20
sorting: [rating]
`

func TestTroikaFinalSeatsFromTheGroupsWithoutASemifinal(t *testing.T) {
	scheme := compileSrc(t, troikaFinalSrc, troikaInput(8))

	final := stageByCode(t, scheme, "s2-final")
	if len(final.Matches) != 3 {
		t.Fatalf("final matches = %d, want a series of 3", len(final.Matches))
	}
	for k, match := range final.Matches {
		if match.Title != fmt.Sprintf("Финал. Бой %d", k+1) {
			t.Fatalf("final match %d title = %q", k, match.Title)
		}
		if match.Slots[0].Reseed.Stage != "s1-g1" || match.Slots[0].Reseed.Rank != 1 ||
			match.Slots[1].Reseed.Stage != "s1-g2" || match.Slots[1].Reseed.Rank != 1 {
			t.Fatalf("final match %d slots: %+v %+v", k, match.Slots[0], match.Slots[1])
		}
	}

	bronze := stageByCode(t, scheme, "s2-bronze")
	if len(bronze.Matches) != 3 {
		t.Fatalf("bronze matches = %d, want a series of 3", len(bronze.Matches))
	}
	bm := bronze.Matches[0].Slots
	if bm[0].Reseed.Stage != "s1-g1" || bm[0].Reseed.Rank != 2 ||
		bm[1].Reseed.Stage != "s1-g2" || bm[1].Reseed.Rank != 2 {
		t.Fatalf("bronze slots: %+v %+v", bm[0], bm[1])
	}
	if bronze.Position >= final.Position {
		t.Fatalf("bronze at %d, final at %d: bronze is played first", bronze.Position, final.Position)
	}
}

// The группа blocks write the регламент verbatim: 1 / 0.5 / 0 плюс очки/50,
// ranked on the рейтинговый балл, then личная встреча, забитые, разница.
const troikaGroupSrc = `
[scheme]
kind: roundrobin
groups: 2
group_size: 4
proceeding_participants: 2
themes: 8
metric: total
points: [1, 0.5, 0]
standings.rating: points + taken / 50
sorting: [rating, h2h, taken, diff]
`

func TestTroikaGroupCarriesFractionalPointsAndItsRatingRule(t *testing.T) {
	scheme := compileSrc(t, troikaGroupSrc, troikaInput(8))
	config := stageConfig(t, stageByCode(t, scheme, "s1-g1"))

	points, ok := config["points"].(map[string]any)
	if !ok {
		t.Fatalf("points = %#v", config["points"])
	}
	if points["win"] != float64(1) || points["draw"] != 0.5 || points["loss"] != float64(0) {
		t.Fatalf("points = %+v, want 1 / 0.5 / 0", points)
	}
	if config["metric"] != "total" {
		t.Fatalf("metric = %v, want total", config["metric"])
	}
	rules, ok := config["rules"].(map[string]any)
	if !ok {
		t.Fatalf("rules = %#v", config["rules"])
	}
	standings := rules["standings"].(map[string]any)
	if standings["rating"] != "points + taken / 50" {
		t.Fatalf("standings.rating = %v", standings["rating"])
	}
	order, _ := config["order"].([]any)
	if len(order) != 4 || order[0] != "rating" || order[1] != "h2h" {
		t.Fatalf("order = %v", order)
	}
}

// Троечка §4.4.2: «номер посева определяется средним арифметическим суммы
// мест в „Вопросиках“ и „Командном своячке“ команд игроков», and §4.4.3
// breaks a tie on the best single сумма мест. A Participant here is three
// people from three other teams, so the посев is composed over them.
const troikaSeedSrc = `
[init]
seed: players
games: [вопросики, своячок]
player.place_sum: place1 + place2
seed.mean: mean(place_sum)
seed.best: min(place_sum)
sorting: [mean asc, best asc]

[scheme]
kind: roundrobin
groups: 2
group_size: 4
themes: 6
`

func TestTroikaSeedComposesOverPlayers(t *testing.T) {
	scheme := compileSrc(t, troikaSeedSrc, troikaInput(8))
	if scheme.Seeding == nil || scheme.Seeding.Source != "players" {
		t.Fatalf("seeding = %+v", scheme.Seeding)
	}
	players := scheme.Seeding.Players
	if players == nil {
		t.Fatal("seed: players carries no player spec")
	}
	if len(players.Games) != 2 || players.Games[0] != "вопросики" || players.Games[1] != "своячок" {
		t.Fatalf("games = %v", players.Games)
	}
	if players.Player["place_sum"] != "place1 + place2" {
		t.Fatalf("player rules = %v", players.Player)
	}
	if players.Seed["mean"] != "mean(place_sum)" || players.Seed["best"] != "min(place_sum)" {
		t.Fatalf("seed rules = %v", players.Seed)
	}
	if len(scheme.Seeding.Sort) != 2 || scheme.Seeding.Sort[0].Metric != "mean" || scheme.Seeding.Sort[0].Dir != "asc" {
		t.Fatalf("sort = %+v", scheme.Seeding.Sort)
	}
}

func TestPlayerSeedNeedsItsSources(t *testing.T) {
	for _, src := range []string{
		"[init]\nseed: players\nseed.mean: mean(x)\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n",
		"[init]\nseed: players\ngames: [вопросики]\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n",
		"[init]\nseed: players\ngames: [вопросики]\nplayer.x: place1 +\nseed.mean: mean(x)\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n",
	} {
		doc, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Compile(doc, troikaInput(4)); err == nil {
			t.Errorf("Compile(%q) = nil error, want a complaint", src)
		}
	}
}

// A series is a ranking scope by default — that is what lets Троечка's финал
// be won on summed рейтинговые баллы. A tournament that reads its финал off
// the бои themselves, as СтудЧР's брейн does, writes rollout and gets them.
func TestRolloutDrawsASeriesAsItsBouts(t *testing.T) {
	const src = `
[scheme]
kind: roundrobin
groups: 2
group_size: 4
proceeding_participants: 2
themes: 6
---
kind: single_elimination
participants: 2
bronze: true
best_of.final: 3
best_of.bronze: 3
%s
`
	ranked := compileSrc(t, fmt.Sprintf(src, ""), troikaInput(8))
	if got := stageByCode(t, ranked, "s2-final").Kind; got != "series" {
		t.Fatalf("final kind = %q, want series", got)
	}
	if got := stageByCode(t, ranked, "s2-bronze").Kind; got != "series" {
		t.Fatalf("bronze kind = %q, want series", got)
	}

	// rollout on one Round leaves the other a ranking scope.
	one := compileSrc(t, fmt.Sprintf(src, "rollout.final: true"), troikaInput(8))
	if got := stageByCode(t, one, "s2-final").Kind; got != "matches" {
		t.Fatalf("rolled-out final kind = %q, want matches", got)
	}
	if got := stageByCode(t, one, "s2-bronze").Kind; got != "series" {
		t.Fatalf("bronze kind = %q, want series", got)
	}
	// Rolling out changes how the бои are read, never which бои are played.
	if a, b := stageByCode(t, ranked, "s2-final"), stageByCode(t, one, "s2-final"); len(a.Matches) != len(b.Matches) {
		t.Fatalf("rollout changed the бои: %d vs %d", len(a.Matches), len(b.Matches))
	}

	// The block-wide form covers every series it has.
	both := compileSrc(t, fmt.Sprintf(src, "rollout: true"), troikaInput(8))
	if got := stageByCode(t, both, "s2-final").Kind; got != "matches" {
		t.Fatalf("block rollout final kind = %q, want matches", got)
	}
	if got := stageByCode(t, both, "s2-bronze").Kind; got != "matches" {
		t.Fatalf("block rollout bronze kind = %q, want matches", got)
	}
}
