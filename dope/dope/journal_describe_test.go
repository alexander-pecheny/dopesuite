package main

import (
	"encoding/json"
	"testing"
)

func mkPatchOp(t *testing.T, pathJSON, valueJSON string) gameStatePatchOp {
	t.Helper()
	var p []json.RawMessage
	if err := json.Unmarshal([]byte(pathJSON), &p); err != nil {
		t.Fatalf("bad path: %v", err)
	}
	return gameStatePatchOp{Op: "set", Path: p, Value: json.RawMessage(valueJSON)}
}

// KSI state shape: themes[t].answers[participant][question] = mark. The
// participant index resolves to a name for display.
func TestKSIPatchLineResolvesParticipant(t *testing.T) {
	r := &nameResolver{gameType: "ksi", names: []string{"Аня", "Боря", "Витя"}}
	got := r.ksiPatchLine(mkPatchOp(t, `["themes",3,"answers",1,2]`, `"wrong"`))
	if got != "тема 4, Боря, вопрос 3: неверно" {
		t.Fatalf("got %q", got)
	}
	// Unknown participant falls back to a number.
	got2 := r.ksiPatchLine(mkPatchOp(t, `["themes",0,"answers",9,0]`, `"right"`))
	if got2 != "тема 1, участник 10, вопрос 1: верно" {
		t.Fatalf("got %q", got2)
	}
}

// OD state shape: entries[question][teamRow] = value. The team row resolves to
// a team name.
func TestODPatchLineResolvesTeam(t *testing.T) {
	r := &nameResolver{gameType: "od", names: []string{"Кратон", "Дятлы"}}
	got := r.odPatchLine(mkPatchOp(t, `["entries",5,1]`, `"+50"`))
	if got != "Дятлы, вопрос 6 → +50" {
		t.Fatalf("got %q", got)
	}
}

func TestParticipantNamesFromState(t *testing.T) {
	ksi := ksiParticipantNames(`{"participants":["Аня",{"name":"Боря"},""]}`)
	if len(ksi) != 3 || ksi[0] != "Аня" || ksi[1] != "Боря" {
		t.Fatalf("ksi names %#v", ksi)
	}
	od := odTeamNames(`{"teams":[{"name":"Кратон"},{"name":"Дятлы"}]}`)
	if len(od) != 2 || od[1] != "Дятлы" {
		t.Fatalf("od names %#v", od)
	}
}
