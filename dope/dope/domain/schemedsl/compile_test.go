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
// best-of-three final. NB the tournament sheet sorted its пересев % взятых
// before разница, against регламент 3.3.5 — this DSL follows the регламент.
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
	if len(sortRules) != 4 || sortRules[3]["metric"] != "draw" {
		t.Fatalf("reseed sort = %v, want жребий last", sortRules)
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
	// The crossed Group takes the same four, listed in source-Group order rather
	// than its own first. Both seat the same people; only own-first pairs them in
	// a different заход, which is what the СтудЧР sheets caught.
	config = stageConfig(t, stageByCode(t, scheme, "s4-g2"))
	wantCross = []struct {
		stage string
		rank  int
	}{{"s3-g1", 2}, {"s3-g2", 1}, {"s3-g3", 2}, {"s3-g4", 1}}
	for i, want := range wantCross {
		entrant := config["entrants"].([]any)[i].(map[string]any)["reseed"].(map[string]any)
		if entrant["stage"] != want.stage || int(entrant["rank"].(float64)) != want.rank {
			t.Fatalf("s4-g2 entrant %d = %v, want %+v", i, entrant, want)
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
	want := []map[string]string{{"metric": "taken", "dir": "desc"}, {"metric": "place_sum", "dir": "asc"}, {"metric": "draw", "dir": "asc"}}
	if len(rules) != 3 || rules[0]["metric"] != want[0]["metric"] || rules[1]["metric"] != want[1]["metric"] || rules[1]["dir"] != "asc" || rules[2]["metric"] != "draw" {
		t.Fatalf("reseed sort = %v, want %v", rules, want)
	}
}

// Which way a metric reads is a property of the metric: место is lower-better,
// очки are higher-better, and a scheme that names one without a direction means
// the obvious one. Naming a direction still wins.
func TestSortingDirectionFollowsTheMetric(t *testing.T) {
	src := "[scheme]\ntype: roundrobin\ngroups: 2\nteams_in_group: 4\nproceeding_teams: 2\n---\n" +
		"type: roundrobin\ngroups: 2\nteams_in_group: 2\nreseed: true\nsorting: [place_sum, taken, place_sum desc]\n"
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	var rules []map[string]string
	if err := json.Unmarshal(stageByCode(t, scheme, "s2-reseed").Sort, &rules); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"asc", "desc", "desc"} {
		if rules[i]["dir"] != want {
			t.Errorf("%s: направление %q, want %q", rules[i]["metric"], rules[i]["dir"], want)
		}
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
		{"se uneven rounds", "[scheme]\ntype: single_elimination\nteams: 6\n", "не делятся"},
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

// A sorting key is any metric the game's Protocol declares — no Go change is
// needed to rank on one. takenBase is brain's; ЭК's Σ+ is not, in a brain game.
func TestSortingAcceptsAnyDeclaredMetric(t *testing.T) {
	src := func(metric string) string {
		return "[scheme]\ntype: roundrobin\ngroups: 2\nteams_in_group: 4\nsorting: [points, " + metric + "]\n"
	}
	scheme := compileSrc(t, src("takenBase"), Input{GameType: "brain"})
	stage := scheme.Stages[0]
	var conf struct {
		Order []string `json:"order"`
	}
	if err := json.Unmarshal(stage.Config, &conf); err != nil {
		t.Fatal(err)
	}
	if len(conf.Order) != 2 || conf.Order[1] != "takenBase" {
		t.Fatalf("order = %v, want [points takenBase]", conf.Order)
	}

	doc, err := Parse(src("shootoutTotal"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(doc, Input{GameType: "brain"}); err == nil {
		t.Fatal("a metric no protocol measures must be a compile error")
	}
}

// ЭК's bracket is a single elimination of four-seat бои where two proceed —
// the same Kind as a classic bracket, at a different size. The 1/4 is played
// three to a table, so the size is a per-round override.
func TestCompileMultiSeatElimination(t *testing.T) {
	src := `
[scheme]
type: single_elimination
teams: 48
match_size: 4
winning_places: 2
match_size.r3: 3
`
	scheme := compileSrc(t, src, Input{GameType: "ek"})
	want := []struct {
		bouts, seats int
	}{{12, 4}, {6, 4}, {4, 3}, {2, 4}, {1, 4}}
	if len(scheme.Stages) != len(want) {
		t.Fatalf("stages = %d, want %d", len(scheme.Stages), len(want))
	}
	for i, w := range want {
		stage := scheme.Stages[i]
		if len(stage.Matches) != w.bouts {
			t.Fatalf("round %d: %d боёв, want %d", i+1, len(stage.Matches), w.bouts)
		}
		for _, match := range stage.Matches {
			if len(match.Slots) != w.seats {
				t.Fatalf("round %d бой %s: %d мест, want %d", i+1, match.Code, len(match.Slots), w.seats)
			}
		}
	}
	// The second round's first бой seats the first two бои's qualifiers.
	second := scheme.Stages[1].Matches[0]
	from := make([]string, len(second.Slots))
	for i, slot := range second.Slots {
		from[i] = fmt.Sprintf("%s#%d", slot.FromMatch.Match, slot.FromMatch.Place)
	}
	got := strings.Join(from, " ")
	if got != "s1-r1-m1#1 s1-r1-m1#2 s1-r1-m2#1 s1-r1-m2#2" {
		t.Fatalf("вторая раунд, бой 1 = %s", got)
	}
}

// Личная СИ's play-off is one double elimination: 24 игрока, бой на четверых,
// проходят двое, и пересев после каждого раунда. Seven rounds, 24 бои, and a
// grand final that hands out places 1–4 — all of it derived.
func TestCompileStudchrSIPlayoff(t *testing.T) {
	src := `
[defaults]
venues: 3
[scheme]
type: double_elimination
teams: 24
match_size: 4
winning_places: 2
reseed: true
sorting: [place_sum, total, plus]
`
	scheme := compileSrc(t, src, Input{GameType: "ksi"})
	var rounds []store.SchemeStage
	reseeds := 0
	for _, stage := range scheme.Stages {
		switch stage.StageType {
		case "reseed":
			reseeds++
		default:
			rounds = append(rounds, stage)
		}
	}
	wantBouts := []int{6, 6, 5, 3, 2, 1, 1}
	if len(rounds) != len(wantBouts) {
		t.Fatalf("раундов = %d, want %d", len(rounds), len(wantBouts))
	}
	for i, want := range wantBouts {
		if len(rounds[i].Matches) != want {
			t.Fatalf("раунд %d: %d боёв, want %d", i+1, len(rounds[i].Matches), want)
		}
	}
	if reseeds != len(rounds)-1 {
		t.Fatalf("пересевов = %d, want %d — по одному между раундами", reseeds, len(rounds)-1)
	}
	// ПО-3 seats its upper bracket three to a table and its lower four.
	third := rounds[2].Matches
	if len(third[0].Slots) != 3 || len(third[2].Slots) != 4 {
		t.Fatalf("ПО-3: верхняя по %d, нижняя по %d — want 3 и 4", len(third[0].Slots), len(third[2].Slots))
	}
	// The grand final seats four and hands out four places.
	final := rounds[len(rounds)-1].Matches[0]
	if len(final.Slots) != 4 {
		t.Fatalf("грандфинал на %d мест, want 4", len(final.Slots))
	}
}

// ОД и КСИ — это один блок и один бой, за которым сидят все. Kind у них
// теперь есть, и схема умеет это сказать.
func TestCompileFlat(t *testing.T) {
	scheme := compileSrc(t, "[scheme]\ntype: flat\nteams: 90\ntitle: КВРМ\n", Input{GameType: "od"})
	if len(scheme.Stages) != 1 {
		t.Fatalf("stages = %d, want 1", len(scheme.Stages))
	}
	stage := scheme.Stages[0]
	if stage.Kind != "flat" || stage.Title != "КВРМ" || len(stage.Matches) != 1 {
		t.Fatalf("stage = %+v", stage)
	}
	if got := len(stage.Matches[0].Slots); got != 90 {
		t.Fatalf("за столом %d, want 90", got)
	}
	if stage.Matches[0].ParticipantCount != 90 {
		t.Fatalf("participantCount = %d, want 90", stage.Matches[0].ParticipantCount)
	}
}

// Группа личной СИ: девять игроков, бой на троих, четыре круга, «4 − место»
// прописано выражением — и по нему же можно сортировать.
func TestCompileSIGroupStage(t *testing.T) {
	src := `
[scheme]
type: roundrobin
groups: 6
teams_in_group: 9
match_size: 3
proceeding_teams: 4
bout.points: seats + 1 - place
sorting: [points, total, plus]
`
	scheme := compileSrc(t, src, Input{GameType: "ksi"})
	if len(scheme.Stages) != 6 {
		t.Fatalf("групп = %d, want 6", len(scheme.Stages))
	}
	group := scheme.Stages[0]
	if len(group.Matches) != 12 {
		t.Fatalf("боёв в группе = %d, want 12", len(group.Matches))
	}
	for _, match := range group.Matches {
		if len(match.Slots) != 3 {
			t.Fatalf("бой %s на %d мест, want 3", match.Code, len(match.Slots))
		}
	}
	var conf struct {
		MatchSize int      `json:"matchSize"`
		Order     []string `json:"order"`
		Rules     struct {
			Bout map[string]string `json:"bout"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(group.Config, &conf); err != nil {
		t.Fatal(err)
	}
	if conf.MatchSize != 3 {
		t.Fatalf("matchSize = %d, want 3", conf.MatchSize)
	}
	if conf.Rules.Bout["points"] != "seats + 1 - place" {
		t.Fatalf("правило очков = %q", conf.Rules.Bout["points"])
	}
	if len(conf.Order) != 3 || conf.Order[0] != "points" {
		t.Fatalf("order = %v", conf.Order)
	}
}

// Правило подсчёта определяет метрику, и по ней сразу можно сортировать —
// вот та самая брейновая раскладка 3/2/1/0.
func TestSchemeDefinedMetricIsRankable(t *testing.T) {
	src := `
[scheme]
type: roundrobin
groups: 2
teams_in_group: 4
bout.points: taken == 0 ? 0 : (tied > 0 ? 2 : (place == 1 ? 3 : 1))
standings.take_rate: bouts > 0 ? taken / bouts : 0
sorting: [points, take_rate]
`
	scheme := compileSrc(t, src, Input{GameType: "brain"})
	var conf struct {
		Order []string `json:"order"`
	}
	if err := json.Unmarshal(scheme.Stages[0].Config, &conf); err != nil {
		t.Fatal(err)
	}
	if len(conf.Order) != 2 || conf.Order[1] != "take_rate" {
		t.Fatalf("order = %v, want [points take_rate]", conf.Order)
	}

	doc, err := Parse("[scheme]\ntype: roundrobin\nteams_in_group: 4\nbout.points: 4 - \n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(doc, Input{GameType: "brain"}); err == nil {
		t.Fatal("сломанное выражение должно быть ошибкой компиляции")
	}
}

// Раунд может назваться сам: у ЭК двенадцать боёв на четверых зовут «1/16
// финала», потому что так их зовёт турнир, а не потому что это следует из
// арифметики.
func TestRoundTitleOverride(t *testing.T) {
	src := `
[scheme]
type: single_elimination
teams: 16
match_size: 4
winning_places: 2
title.r1: 1/8 финала
title.r2: Полуфиналы
`
	scheme := compileSrc(t, src, Input{GameType: "ek"})
	want := []string{"1/8 финала", "Полуфиналы", "Финал"}
	for i, title := range want {
		if scheme.Stages[i].Title != title {
			t.Fatalf("раунд %d = %q, want %q", i+1, scheme.Stages[i].Title, title)
		}
	}
}
