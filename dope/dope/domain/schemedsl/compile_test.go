package schemedsl

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dope/dope/storage/store"
)

func compileSrc(t *testing.T, src string, in Input) store.FestScheme {
	t.Helper()
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := Compile(doc, in)
	if err != nil {
		t.Fatal(err)
	}
	return scheme
}

func stageByCode(t *testing.T, scheme store.FestScheme, code string) store.SchemeStage {
	t.Helper()
	for _, stage := range scheme.Stages {
		if stage.Code == code {
			return stage
		}
	}
	t.Fatalf("stage %s not found among %d stages", code, len(scheme.Stages))
	return store.SchemeStage{}
}

func stageConfig(t *testing.T, stage store.SchemeStage) map[string]any {
	t.Helper()
	var config map[string]any
	if err := json.Unmarshal(stage.Config, &config); err != nil {
		t.Fatalf("%s config: %v", stage.Code, err)
	}
	return config
}

const singleGroupSrc = `
[defaults]
questions: 5

[scheme]
type: roundrobin
teams_in_group: 4
`

func brainInput(entrants ...string) Input {
	in := Input{Slug: "brain-1", Title: "Брейн", GameType: "brain"}
	for i, name := range entrants {
		in.Entrants = append(in.Entrants, store.SchemeSlot{
			Seed:  &store.SchemeSeedRef{Basket: 1, Number: i + 1},
			Label: name,
		})
	}
	return in
}

// The one-group brain scheme must expand exactly the way createBrainGameTx
// builds it today: one rr stage, canonical KINSBF rounds, entrants seated in
// seed order.
func TestCompileSingleGroupBrain(t *testing.T) {
	scheme := compileSrc(t, singleGroupSrc, brainInput("Яблоко", "Ель", "Берёза", "Астра"))
	if scheme.GameType != "brain" || scheme.Questions != 5 {
		t.Fatalf("gameType=%s questions=%d", scheme.GameType, scheme.Questions)
	}
	if len(scheme.Stages) != 1 {
		t.Fatalf("stages = %d", len(scheme.Stages))
	}
	stage := stageByCode(t, scheme, "s1-g1")
	if stage.Kind != "rr" || stage.StageType != "matches" {
		t.Fatalf("kind=%s type=%s", stage.Kind, stage.StageType)
	}
	if len(stage.Matches) != 6 {
		t.Fatalf("matches = %d", len(stage.Matches))
	}
	if stage.Matches[0].Code != "s1-g1-1" {
		t.Fatalf("first match code = %s", stage.Matches[0].Code)
	}
	// Canonical KINSBF round one: 1-2, 3-4.
	first := stage.Matches[0].Slots
	if first[0].Label != "Яблоко" || first[1].Label != "Ель" {
		t.Fatalf("round one pairing: %+v", first)
	}
	second := stage.Matches[1].Slots
	if second[0].Label != "Берёза" || second[1].Label != "Астра" {
		t.Fatalf("round one pairing: %+v", second)
	}
	config := stageConfig(t, stage)
	if config["questions"] != float64(5) {
		t.Fatalf("stage questions = %v", config["questions"])
	}
	entrants, _ := config["entrants"].([]any)
	if len(entrants) != 4 {
		t.Fatalf("config entrants = %v", config["entrants"])
	}
}

// kinsbfSrc is the actual СтудЧР-2026 КИНСБФ scheme (регламент разделы 3–4,
// verified against the tournament's gsheet tables and generate_kinsbf.py):
// basket lottery, 12 groups over 6 столов in two захода, six 4-team DE groups,
// пересев by 3.3.5 over group+DE stats, PF snake, alternating cross, and a
// best-of-three final.
const kinsbfSrc = `
[defaults]
venues: 6
questions: 5

[init]
seed: xlsx

[scheme]
title: 1-й групповой этап
type: roundrobin
groups: 12
teams_in_group: 4
proceeding_teams: 2
---
type: double_elimination
groups: 6
proceeding_teams: 2
---
title: 2-й групповой этап
type: roundrobin
groups: 4
teams_in_group: 3
reseed: true
stats_from: [s1, s2]
sorting: [points_share desc, diff desc, taken_share desc]
proceeding_teams: 2
---
title: 3-й групповой этап
type: roundrobin
groups: 2
teams_in_group: 4
proceeding_teams: 2
---
type: single_elimination
teams: 4
bronze: true
questions: 7
questions.final: 5
best_of.final: 3
`

func TestCompileKinsbf(t *testing.T) {
	scheme := compileSrc(t, kinsbfSrc, Input{Slug: "brain-1", Title: "Брейн", GameType: "brain"})

	if scheme.Seeding == nil || scheme.Seeding.Source != "xlsx" {
		t.Fatalf("seeding = %+v", scheme.Seeding)
	}
	if len(scheme.Venues) != 6 {
		t.Fatalf("venues = %d", len(scheme.Venues))
	}
	// 12 groups + 6 DE groups + reseed + 4 groups + 2 groups + semifinal + final + bronze.
	if len(scheme.Stages) != 28 {
		t.Fatalf("stages = %d", len(scheme.Stages))
	}

	// Block 1: seeds snake-dealt across baskets of 12.
	g1 := stageByCode(t, scheme, "s1-g1")
	if g1.Title != "1-й групповой этап. Группа 1" {
		t.Fatalf("s1-g1 title = %q", g1.Title)
	}
	wantSeeds := []int{1, 24, 25, 48}
	config := stageConfig(t, g1)
	entrants := config["entrants"].([]any)
	for i, want := range wantSeeds {
		seed := entrants[i].(map[string]any)["seed"].(map[string]any)
		if int(seed["position"].(float64)) != want {
			t.Fatalf("s1-g1 entrant %d = %v, want position %d", i, seed, want)
		}
	}
	if g1.Matches[0].Venue != 1 {
		t.Fatalf("s1-g1 venue = %d", g1.Matches[0].Venue)
	}
	if stageByCode(t, scheme, "s1-g7").Matches[0].Venue != 1 {
		t.Fatalf("venues must cycle: s1-g7 got %d", stageByCode(t, scheme, "s1-g7").Matches[0].Venue)
	}

	// Block 2: DE group 1 draws rows (wave pairs) — s1-g1/1, s1-g2/2, s1-g7/1, s1-g8/2.
	de1 := stageByCode(t, scheme, "s2-g1")
	if de1.Kind != "matches" || de1.StageType != "matches" || len(de1.Matches) != 5 {
		t.Fatalf("de stage: kind=%s matches=%d", de1.Kind, len(de1.Matches))
	}
	m1 := de1.Matches[0].Slots
	if m1[0].Reseed.Stage != "s1-g1" || m1[0].Reseed.Rank != 1 || m1[1].Reseed.Stage != "s1-g2" || m1[1].Reseed.Rank != 2 {
		t.Fatalf("de m1 slots: %+v %+v", m1[0].Reseed, m1[1].Reseed)
	}
	m2 := de1.Matches[1].Slots
	if m2[0].Reseed.Stage != "s1-g7" || m2[1].Reseed.Stage != "s1-g8" {
		t.Fatalf("de m2 slots: %+v %+v", m2[0].Reseed, m2[1].Reseed)
	}
	m5 := de1.Matches[4].Slots
	if m5[0].FromMatch.Match != "s2-g1-m3" || m5[0].FromMatch.Place != 2 {
		t.Fatalf("de m5 slot 0: %+v", m5[0].FromMatch)
	}
	if m5[1].FromMatch.Match != "s2-g1-m4" || m5[1].FromMatch.Place != 1 {
		t.Fatalf("de m5 slot 1: %+v", m5[1].FromMatch)
	}

	// Block 3: reseed edge over the six DE groups, then PF_GROUPS snake.
	reseed := stageByCode(t, scheme, "s3-reseed")
	if reseed.StageType != "reseed" {
		t.Fatalf("reseed stage type = %s", reseed.StageType)
	}
	// Регламент 3.3.5: stats over the group stage AND the DE, eligibility =
	// the 12 DE qualifiers (w(бой 3), w(бой 5) per group), rates order.
	if len(reseed.Sources) != 18 || reseed.Sources[0] != "s1-g1" || reseed.Sources[17] != "s2-g6" {
		t.Fatalf("reseed sources = %v", reseed.Sources)
	}
	if len(reseed.Teams) != 12 {
		t.Fatalf("reseed teams = %d, want 12", len(reseed.Teams))
	}
	if fm := reseed.Teams[0].FromMatch; fm == nil || fm.Match != "s2-g1-m3" || fm.Place != 1 {
		t.Fatalf("reseed team 0 = %+v", reseed.Teams[0])
	}
	if fm := reseed.Teams[1].FromMatch; fm == nil || fm.Match != "s2-g1-m5" || fm.Place != 1 {
		t.Fatalf("reseed team 1 = %+v", reseed.Teams[1])
	}
	var sortRules []map[string]string
	if err := json.Unmarshal(reseed.Sort, &sortRules); err != nil {
		t.Fatal(err)
	}
	wantSort := []string{"points_share", "diff", "taken_share"}
	if len(sortRules) != 3 {
		t.Fatalf("reseed sort = %v", sortRules)
	}
	for i, want := range wantSort {
		if sortRules[i]["metric"] != want || sortRules[i]["dir"] != "desc" {
			t.Fatalf("reseed sort %d = %v, want %s desc", i, sortRules[i], want)
		}
	}
	pf1 := stageByCode(t, scheme, "s3-g1")
	wantRanks := []int{1, 8, 9}
	for i, want := range wantRanks {
		slot := pf1.Matches[0].Slots
		_ = slot
		config := stageConfig(t, pf1)
		entrant := config["entrants"].([]any)[i].(map[string]any)["reseed"].(map[string]any)
		if entrant["stage"] != "s3-reseed" || int(entrant["rank"].(float64)) != want {
			t.Fatalf("s3-g1 entrant %d = %v, want rank %d", i, entrant, want)
		}
	}

	// Block 4: alternating cross template from the four groups.
	f1 := stageByCode(t, scheme, "s4-g1")
	config = stageConfig(t, f1)
	wantCross := []struct {
		stage string
		rank  int
	}{{"s3-g1", 1}, {"s3-g2", 2}, {"s3-g3", 1}, {"s3-g4", 2}}
	for i, want := range wantCross {
		entrant := config["entrants"].([]any)[i].(map[string]any)["reseed"].(map[string]any)
		if entrant["stage"] != want.stage || int(entrant["rank"].(float64)) != want.rank {
			t.Fatalf("s4-g1 entrant %d = %v, want %+v", i, entrant, want)
		}
	}

	// Block 5: semifinals cross group winners, final + bronze chain by place.
	semi := stageByCode(t, scheme, "s5-semifinal")
	if len(semi.Matches) != 2 {
		t.Fatalf("semifinal matches = %d", len(semi.Matches))
	}
	sm1 := semi.Matches[0].Slots
	if sm1[0].Reseed.Stage != "s4-g1" || sm1[0].Reseed.Rank != 1 || sm1[1].Reseed.Stage != "s4-g2" || sm1[1].Reseed.Rank != 2 {
		t.Fatalf("semifinal m1: %+v %+v", sm1[0].Reseed, sm1[1].Reseed)
	}
	// Регламент 3.7: финал — победитель двух боёв из трёх.
	final := stageByCode(t, scheme, "s5-final")
	if len(final.Matches) != 3 {
		t.Fatalf("final matches = %d, want a best-of-3", len(final.Matches))
	}
	for k, match := range final.Matches {
		if match.Code != fmt.Sprintf("s5-final-m%d", k+1) || match.Title != fmt.Sprintf("Финал. Бой %d", k+1) {
			t.Fatalf("final match %d = %s %q", k, match.Code, match.Title)
		}
		if match.Slots[0].FromMatch.Match != "s5-semifinal-m1" || match.Slots[0].FromMatch.Place != 1 ||
			match.Slots[1].FromMatch.Match != "s5-semifinal-m2" || match.Slots[1].FromMatch.Place != 1 {
			t.Fatalf("final match %d slots: %+v %+v", k, match.Slots[0].FromMatch, match.Slots[1].FromMatch)
		}
	}
	bronze := stageByCode(t, scheme, "s5-bronze")
	bm := bronze.Matches[0].Slots
	if bm[0].FromMatch.Match != "s5-semifinal-m1" || bm[0].FromMatch.Place != 2 {
		t.Fatalf("bronze slot 0: %+v", bm[0].FromMatch)
	}

	// The questions cascade: block 5 says 7, the final round overrides to 5.
	if stageConfig(t, semi)["questions"] != float64(7) {
		t.Fatalf("semifinal questions = %v", stageConfig(t, semi)["questions"])
	}
	if stageConfig(t, final)["questions"] != float64(5) {
		t.Fatalf("final questions = %v", stageConfig(t, final)["questions"])
	}
	if stageConfig(t, bronze)["questions"] != float64(7) {
		t.Fatalf("bronze questions = %v", stageConfig(t, bronze)["questions"])
	}
}

func TestCompileVenueTitles(t *testing.T) {
	src := `
[defaults]
venues: [Москва-1, Рим]

[scheme]
type: roundrobin
groups: 2
teams_in_group: 2
`
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	if len(scheme.Venues) != 2 || scheme.Venues[1].Title != "Рим" {
		t.Fatalf("venues = %+v", scheme.Venues)
	}
}

// Four groups feeding an se of 8 must not seat pod-mates adjacently: the
// second opening match comes from the OTHER pod, so pod survivors only meet
// again in the final half.
func TestCompileSECrossPodPairing(t *testing.T) {
	src := `
[scheme]
type: roundrobin
groups: 4
teams_in_group: 4
proceeding_teams: 2
---
type: single_elimination
teams: 8
`
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	r8 := stageByCode(t, scheme, "s2-r8")
	wantPairs := [][2]struct {
		stage string
		rank  int
	}{
		{{"s1-g1", 1}, {"s1-g2", 2}},
		{{"s1-g3", 1}, {"s1-g4", 2}},
		{{"s1-g2", 1}, {"s1-g1", 2}},
		{{"s1-g4", 1}, {"s1-g3", 2}},
	}
	for i, want := range wantPairs {
		slots := r8.Matches[i].Slots
		for side := 0; side < 2; side++ {
			if slots[side].Reseed.Stage != want[side].stage || slots[side].Reseed.Rank != want[side].rank {
				t.Fatalf("r8 m%d side %d = %+v, want %+v", i+1, side, slots[side].Reseed, want[side])
			}
		}
	}
}

func TestCompileWaveSplit(t *testing.T) {
	src := `
[defaults]
venues: 2

[scheme]
type: single_elimination
teams: 8
`
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	w1 := stageByCode(t, scheme, "s1-r8-w1")
	w2 := stageByCode(t, scheme, "s1-r8-w2")
	if len(w1.Matches) != 2 || len(w2.Matches) != 2 {
		t.Fatalf("wave matches = %d + %d, want 2 + 2", len(w1.Matches), len(w2.Matches))
	}
	if w2.Matches[0].Code != "s1-r8-m3" || w2.Matches[0].Venue != 1 {
		t.Fatalf("wave 2 opens with %s at venue %d", w2.Matches[0].Code, w2.Matches[0].Venue)
	}
	semi := stageByCode(t, scheme, "s1-semifinal")
	if len(semi.Matches) != 2 {
		t.Fatalf("semifinal fits one wave, got %d matches", len(semi.Matches))
	}
}

func TestCompileVenueRestriction(t *testing.T) {
	src := `
[defaults]
venues: [Москва-1, Рим]

[scheme]
type: single_elimination
teams: 4
venues.final: [Рим]
`
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	if got := stageByCode(t, scheme, "s1-final").Matches[0].Venue; got != 2 {
		t.Fatalf("final venue = %d, want 2 (Рим)", got)
	}
	if got := stageByCode(t, scheme, "s1-semifinal").Matches[0].Venue; got != 1 {
		t.Fatalf("semifinal venue = %d, want 1", got)
	}
}

// reseed: <round> re-ranks mid-block: the named round seats from a reseed
// stage over the previous round instead of from_match refs. r4 is an accepted
// alias for semifinal (and r2 for final) in every round-addressing key.
func TestCompileIntraBlockReseed(t *testing.T) {
	src := `
[scheme]
type: roundrobin
groups: 4
teams_in_group: 4
proceeding_teams: 2
---
type: single_elimination
teams: 8
reseed: r4
questions.r4: 9
`
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	reseed := stageByCode(t, scheme, "s2-reseed")
	if len(reseed.Sources) != 1 || reseed.Sources[0] != "s2-r8" {
		t.Fatalf("reseed sources = %v, want [s2-r8]", reseed.Sources)
	}
	semi := stageByCode(t, scheme, "s2-semifinal")
	m1 := semi.Matches[0].Slots
	if m1[0].Reseed.Stage != "s2-reseed" || m1[0].Reseed.Rank != 1 || m1[1].Reseed.Rank != 4 {
		t.Fatalf("semifinal m1 = %+v %+v, want reseed ranks 1 vs 4", m1[0].Reseed, m1[1].Reseed)
	}
	if stageConfig(t, semi)["questions"] != float64(9) {
		t.Fatalf("questions.r4 alias did not reach semifinal: %v", stageConfig(t, semi)["questions"])
	}

	// A wave-split previous round must be sourced wave by wave.
	waveSrc := "[defaults]\nvenues: 2\n\n[scheme]\ntype: single_elimination\nteams: 8\nreseed: semifinal\n"
	waved := compileSrc(t, waveSrc, Input{GameType: "brain"})
	wavedReseed := stageByCode(t, waved, "s1-reseed")
	if len(wavedReseed.Sources) != 2 || wavedReseed.Sources[1] != "s1-r8-w2" {
		t.Fatalf("wave reseed sources = %v, want both r8 waves", wavedReseed.Sources)
	}
}

func TestCompileDETeamsAlias(t *testing.T) {
	src := `
[scheme]
type: double_elimination
teams: 8
`
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	stageByCode(t, scheme, "s1-g1")
	stageByCode(t, scheme, "s1-g2")
	if len(scheme.Stages) != 2 {
		t.Fatalf("stages = %d, want 2 DE groups", len(scheme.Stages))
	}
}

func TestCompileReseedSorting(t *testing.T) {
	src := `
[scheme]
type: roundrobin
groups: 2
teams_in_group: 4
proceeding_teams: 2
---
type: roundrobin
groups: 2
teams_in_group: 2
reseed: true
sorting: [taken, points]
`
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	reseed := stageByCode(t, scheme, "s2-reseed")
	var rules []map[string]string
	if err := json.Unmarshal(reseed.Sort, &rules); err != nil {
		t.Fatal(err)
	}
	want := []map[string]string{{"metric": "taken", "dir": "desc"}, {"metric": "place_sum", "dir": "asc"}}
	if len(rules) != 2 || rules[0]["metric"] != want[0]["metric"] || rules[1]["metric"] != want[1]["metric"] || rules[1]["dir"] != "asc" {
		t.Fatalf("reseed sort = %v, want %v", rules, want)
	}
}

func TestCompileInitSorting(t *testing.T) {
	src := "[init]\nseed: kvrm\nsorting: [points desc, rating desc]\n\n[scheme]\ntype: roundrobin\nteams_in_group: 4\n"
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	if scheme.Seeding == nil || scheme.Seeding.Source != "kvrm" {
		t.Fatalf("seeding = %+v", scheme.Seeding)
	}
	if len(scheme.Seeding.Sort) != 2 || scheme.Seeding.Sort[1].Metric != "rating" {
		t.Fatalf("seeding sort = %+v", scheme.Seeding.Sort)
	}
}

// A reseed after an rr block re-ranks the groups' top places — its eligibility
// set is rank refs into the group standings.
func TestCompileReseedEligibilityFromGroups(t *testing.T) {
	src := `
[scheme]
type: roundrobin
groups: 2
teams_in_group: 4
proceeding_teams: 2
---
type: roundrobin
groups: 2
teams_in_group: 2
reseed: true
`
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	reseed := stageByCode(t, scheme, "s2-reseed")
	if len(reseed.Teams) != 4 {
		t.Fatalf("reseed teams = %d, want 4", len(reseed.Teams))
	}
	want := []struct {
		stage string
		rank  int
	}{{"s1-g1", 1}, {"s1-g1", 2}, {"s1-g2", 1}, {"s1-g2", 2}}
	for i, w := range want {
		ref := reseed.Teams[i].Reseed
		if ref == nil || ref.Stage != w.stage || ref.Rank != w.rank {
			t.Fatalf("reseed team %d = %+v, want %+v", i, reseed.Teams[i], w)
		}
	}
}

func TestCompileEntrantCountMismatch(t *testing.T) {
	doc, err := Parse(singleGroupSrc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(doc, brainInput("Один", "Два", "Три"))
	if err == nil || !strings.Contains(err.Error(), "4") || !strings.Contains(err.Error(), "3") {
		t.Fatalf("want count mismatch mentioning 4 and 3, got %v", err)
	}
}

func TestCompileErrors(t *testing.T) {
	cases := []struct {
		name, src, wantSubstr string
	}{
		{"swiss unimplemented", "[scheme]\ntype: swiss\nteams: 8\n", "swiss"},
		{"unknown kind", "[scheme]\ntype: whist\n", "whist"},
		{"unknown key", "[scheme]\ntype: roundrobin\nteam_in_group: 4\n", "team_in_group"},
		{"rr needs size", "[scheme]\ntype: roundrobin\n", "teams_in_group"},
		{"se power of two", "[scheme]\ntype: single_elimination\nteams: 6\n", "степень"},
		{"unknown round suffix", "[scheme]\ntype: single_elimination\nteams: 4\nquestions.r16: 9\n", "r16"},
		{"no proceeding", "[scheme]\ntype: roundrobin\ngroups: 2\nteams_in_group: 4\n---\ntype: single_elimination\nteams: 4\n", "proceeding_teams"},
		{"no deterministic template", "[scheme]\ntype: roundrobin\ngroups: 5\nteams_in_group: 4\nproceeding_teams: 2\n---\ntype: roundrobin\ngroups: 2\nteams_in_group: 5\n", "reseed"},
		{"reseed round on rr", "[scheme]\ntype: roundrobin\ngroups: 2\nteams_in_group: 4\nproceeding_teams: 2\n---\ntype: roundrobin\ngroups: 2\nteams_in_group: 2\nreseed: r4\n", "раунда"},
		{"reseed sorting unmappable", "[scheme]\ntype: roundrobin\ngroups: 2\nteams_in_group: 4\nproceeding_teams: 2\n---\ntype: roundrobin\ngroups: 2\nteams_in_group: 2\nreseed: true\nsorting: [head2head]\n", "пересев"},
		{"best_of outside final", "[scheme]\ntype: single_elimination\nteams: 8\nbest_of.semifinal: 3\n", "только в финале"},
		{"best_of even", "[scheme]\ntype: single_elimination\nteams: 4\nbest_of.final: 2\n", "нечётное"},
		{"block after series", "[scheme]\ntype: single_elimination\nteams: 4\nproceeding_teams: 2\nbest_of.final: 3\n---\ntype: roundrobin\nteams_in_group: 2\n", "серией"},
		{"stats_from without reseed", "[scheme]\ntype: roundrobin\ngroups: 2\nteams_in_group: 4\nproceeding_teams: 2\n---\ntype: roundrobin\ngroups: 2\nteams_in_group: 2\nstats_from: [s1]\n", "reseed: true"},
		{"stats_from unknown block", "[scheme]\ntype: roundrobin\ngroups: 2\nteams_in_group: 4\nproceeding_teams: 2\n---\ntype: roundrobin\ngroups: 2\nteams_in_group: 2\nreseed: true\nstats_from: [s3]\n", "s3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Compile(doc, Input{GameType: "brain"})
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error %q lacks %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}
