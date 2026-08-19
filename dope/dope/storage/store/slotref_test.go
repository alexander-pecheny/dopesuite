package store

import (
	"encoding/json"
	"testing"
)

func seed(n int) SchemeSlot { return SchemeSlot{Seed: &SchemeSeedRef{Number: n}} }

func TestSlotRefWritesTheRowsItAlwaysWrote(t *testing.T) {
	cases := []struct {
		slot     SchemeSlot
		wantType string
		wantJSON string
	}{
		{seed(3), "seed", `{"basket":1,"label":"Посев-3","number":3}`},
		{SchemeSlot{Seed: &SchemeSeedRef{Basket: 2, Position: 4}}, "seed", `{"basket":2,"label":"","number":4}`},
		{SchemeSlot{FromMatch: &SchemeFromMatchRef{Match: "m1", Place: 2}, Label: "2-е из m1"}, "from_match", `{"label":"2-е из m1","match":"m1","place":2}`},
		{SchemeSlot{Reseed: &SchemeReseedRef{Stage: "s1", Rank: 5}}, "reseed", `{"label":"","rank":5,"stage":"s1"}`},
		{SchemeSlot{Placeholder: "tbd", Label: "?"}, "placeholder", `{"label":"?","placeholder":"tbd"}`},
		{SchemeSlot{Label: "free"}, "placeholder", `{"label":"free"}`},
		{SchemeSlot{}, "placeholder", `{}`},
	}
	for _, c := range cases {
		ref := SlotRefOf(c.slot)
		if ref.Type != c.wantType || ref.JSON() != c.wantJSON {
			t.Errorf("SlotRefOf(%+v) = %s %s", c.slot, ref.Type, ref.JSON())
		}
		back := ParseSlotRef(ref.Type, ref.JSON())
		if back.Identity() != ref.Identity() || back.DisplayLabel() != ref.DisplayLabel() {
			t.Errorf("round trip of %s: %+v vs %+v", ref.JSON(), back, ref)
		}
	}
}

func TestParseSlotRefReadsLegacyRows(t *testing.T) {
	ref := ParseSlotRef("seed", `{"position":7}`)
	if ref.Basket != 1 || ref.Number != 7 || ref.Identity() != "seed:1:7" || ref.DisplayLabel() != "К1-7" {
		t.Fatalf("legacy seed: %+v", ref)
	}
	if got := ParseSlotRef("seed", `{"number":2,"label":"seed-2"}`).DisplayLabel(); got != "Посев-2" {
		t.Fatalf("english label: %q", got)
	}
	labels := map[string]string{
		ParseSlotRef("from_match", `{"match":"A","place":1}`).DisplayLabel():             "A1",
		ParseSlotRef("reseed", `{"stage":"s2","rank":3}`).DisplayLabel():                 "Пересев-3",
		ParseSlotRef("placeholder", `{"placeholder":"бай"}`).DisplayLabel():              "бай",
		ParseSlotRef("placeholder", `{}`).DisplayLabel():                                 "Ожидает команды",
		ParseSlotRef("from_match", `{"match":"A","place":1,"label":"x"}`).DisplayLabel(): "x",
	}
	for got, want := range labels {
		if got != want {
			t.Errorf("label %q, want %q", got, want)
		}
	}
	if SeedRef(9).JSON() != `{"basket":1,"label":"","number":9}` {
		t.Fatalf("SeedRef: %s", SeedRef(9).JSON())
	}
}

func TestStageConfigEnvelope(t *testing.T) {
	if got := StageConfigOf(SchemeStage{}).JSON(); got != "{}" {
		t.Fatalf("empty stage: %s", got)
	}
	stage := SchemeStage{Sources: []string{"s0"}, Sort: json.RawMessage(`["pts"]`), Bands: []int{1, 2}, Config: json.RawMessage(`{"questions":5,"themes":7}`)}
	c := StageConfigOf(stage)
	back := ParseStageConfig(c.JSON())
	if back.Questions() != 5 || back.Themes() != 7 || len(back.Sources) != 1 || string(back.KindConfig()) != `{"questions":5,"themes":7}` {
		t.Fatalf("round trip: %+v", back)
	}
	// A Kind whose settings sit at the top level (a reseed's sort) reads the envelope itself.
	raw := `{"sort":[{"metric":"total"}],"teams":[]}`
	if got := string(ParseStageConfig(raw).KindConfig()); got != raw {
		t.Fatalf("top-level kind config: %s", got)
	}
	if ParseStageConfig(`{"questions":4}`).Questions() != 4 {
		t.Fatal("legacy top-level questions not read")
	}
	if ParseStageConfig("").Themes() != 0 || string(ParseStageConfig("").KindConfig()) != "{}" {
		t.Fatal("empty config")
	}
}
