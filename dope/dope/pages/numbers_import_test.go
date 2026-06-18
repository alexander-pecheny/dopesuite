package pages

import (
	"testing"

	"dope/dope/numbering"
)

func TestParseNumberImport(t *testing.T) {
	text := "1\tАльфа\n2\tБета\n\n3\tГамма\nплохая строка\n4\t\n10000\tслишком большой\n2\tдубликат"
	entries, errs := parseNumberImport(text)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Number != 1 || entries[0].Raw != "Альфа" {
		t.Errorf("entry 0 = %+v", entries[0])
	}
	if entries[2].Number != 3 || entries[2].Raw != "Гамма" {
		t.Errorf("entry 2 = %+v", entries[2])
	}
	// "плохая строка" (no number), "4\t" (empty name), "10000" (out of range),
	// "2 дубликат" (duplicate number) -> 4 errors.
	if len(errs) != 4 {
		t.Errorf("expected 4 errors, got %d: %v", len(errs), errs)
	}
}

func TestSplitNumberLineSpaceFallback(t *testing.T) {
	num, name := splitNumberLine("7 Команда мечты")
	if num != "7" || name != "Команда мечты" {
		t.Errorf("got num=%q name=%q", num, name)
	}
}

func TestMatchNumberImport(t *testing.T) {
	teams := []numbering.Team{
		{ID: 11, Name: "Альфа", City: "Москва"},
		{ID: 22, Name: "Бета", City: "Питер"},
		{ID: 33, Name: "Гамма", City: ""},
	}
	entries := []importEntry{
		{Line: 1, Number: 1, Raw: "Гамма"}, // exact
		{Line: 2, Number: 2, Raw: "Бетта"}, // fuzzy -> Бета
		{Line: 3, Number: 3, Raw: "Альфа"}, // exact
	}
	matches := matchNumberImport(entries, teams)
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(matches))
	}
	byNumber := map[int]importMatch{}
	for _, m := range matches {
		byNumber[m.Number] = m
	}
	if m := byNumber[1]; m.TeamID != 33 || !m.Exact {
		t.Errorf("number 1 -> %+v, want team 33 exact", m)
	}
	if m := byNumber[2]; m.TeamID != 22 || m.Exact || m.Distance == 0 {
		t.Errorf("number 2 -> %+v, want fuzzy team 22", m)
	}
	if m := byNumber[3]; m.TeamID != 11 || !m.Exact {
		t.Errorf("number 3 -> %+v, want team 11 exact", m)
	}
}

func TestMatchNumberImportNoDoubleAssign(t *testing.T) {
	teams := []numbering.Team{
		{ID: 11, Name: "Альфа"},
		{ID: 22, Name: "Бета"},
	}
	// Two pasted names both closest to "Альфа"; only one team may win it, the
	// other must fall to the remaining team (or no match).
	entries := []importEntry{
		{Line: 1, Number: 1, Raw: "Альфа"},
		{Line: 2, Number: 2, Raw: "Альфаа"},
	}
	matches := matchNumberImport(entries, teams)
	seen := map[int64]bool{}
	for _, m := range matches {
		if m.TeamID == 0 {
			continue
		}
		if seen[m.TeamID] {
			t.Errorf("team %d assigned twice", m.TeamID)
		}
		seen[m.TeamID] = true
	}
	if !seen[11] {
		t.Errorf("exact match for team 11 should have won, matches=%+v", matches)
	}
}
