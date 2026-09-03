package hostpages

import (
	"strings"
	"testing"

	"dope/dope/domain/venues"
	"dope/dope/storage/store"
)

func TestContestedRowsNumberByTour(t *testing.T) {
	rows := contestedRows(
		[]store.ContestedAnswer{{Question: 1, Number: 3}, {Question: 13, Number: 1, AcceptedHere: true}},
		`{"tourComp":[12,12]}`,
		`{"teams":[{"name":"Мантисса","number":1},{"name":"Вторая","number":3}]}`,
	)
	if rows[0].Tour != 1 || rows[0].InTour != 2 || rows[0].TeamName != "Вторая" {
		t.Fatalf("first %+v", rows[0])
	}
	if rows[1].Tour != 2 || rows[1].InTour != 2 || rows[1].TeamName != "Мантисса" {
		t.Fatalf("second %+v", rows[1])
	}
}

func TestSlotContestedSectionOffersTheToggleAndTheDelete(t *testing.T) {
	data := slotPageData{
		Venue: venues.Venue{ID: 1, Slug: "tbilisi", Title: "Площадка"},
		Slot:  venues.Slot{ID: 7, FestID: 1, GameID: 3},
		Contested: []ContestedRow{{
			ContestedAnswer: store.ContestedAnswer{Question: 1, Number: 3, Answer: "Текст"},
			Tour:            1, InTour: 2, TeamName: "Вторая",
		}},
		CanManage: true,
	}
	body := renderPublic(t, slotPageDoc(data))
	for _, want := range []string{
		"Спорные", "Текст", "3 · Вторая",
		"/host/venue/tbilisi/game/3/contested/accept",
		"/host/venue/tbilisi/game/3/contested/delete",
		"Принять на площадке",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	data.Contested[0].AcceptedHere = true
	if body := renderPublic(t, slotPageDoc(data)); !strings.Contains(body, "Снять принятие") {
		t.Error("an accepted спорный offers to be un-accepted")
	}
	data.CanManage = false
	if body := renderPublic(t, slotPageDoc(data)); strings.Contains(body, "contested/delete") {
		t.Error("a host without editor rights gets no buttons")
	}
	empty := slotPageData{Venue: data.Venue, Slot: data.Slot}
	if body := renderPublic(t, slotPageDoc(empty)); !strings.Contains(body, "Спорных нет.") {
		t.Error("missing the empty note")
	}
}
