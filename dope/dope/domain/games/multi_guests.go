package games

import (
	"encoding/json"
	"strings"

	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// A Multi game's guest teams (MultiGuest) are the host's to add, rename
// and remove on the game's own page. Each edit rewrites the document the way
// a roster fold does — the list in the scheme and the state, every team's
// cells following it to its new row — so the fold and these edits never
// disagree about where a team's points are.

// AddMultiGuest seats a new guest team after the game's other teams, under
// the next Number below every guest's so far.
func AddMultiGuest(schemeJSON, stateJSON, name string) ([]byte, []byte, error) {
	state, participants, err := multiGuestDoc(stateJSON)
	if err != nil {
		return nil, nil, err
	}
	name, err = guestName(participants, name, 0)
	if err != nil {
		return nil, nil, err
	}
	number := 0
	for _, p := range participants {
		number = min(number, p.Number)
	}
	next := append(append([]KSIParticipant(nil), participants...), KSIParticipant{Number: number - 1, Name: name})
	return rewriteMultiParticipants(schemeJSON, state, participants, next)
}

// RenameMultiGuest renames the guest team numbered number. Its cells and its
// refusal are keyed on the Number, so they stay where they are.
func RenameMultiGuest(schemeJSON, stateJSON string, number int, name string) ([]byte, []byte, error) {
	state, participants, err := multiGuestDoc(stateJSON)
	if err != nil {
		return nil, nil, err
	}
	at := guestIndex(participants, number)
	if at < 0 {
		return nil, nil, corei18n.User(dopestrings.Default.Games.MultiGuest.NotFound())
	}
	if name, err = guestName(participants, name, number); err != nil {
		return nil, nil, err
	}
	next := append([]KSIParticipant(nil), participants...)
	next[at].Name = name
	return rewriteMultiParticipants(schemeJSON, state, participants, next)
}

// RemoveMultiGuest takes the guest team numbered number off the game. A team
// with anything entered — a point in any cell, or a refusal — is refused: the
// host clears it first, so a slip of the hand never loses a sheet.
func RemoveMultiGuest(schemeJSON, stateJSON string, number int) ([]byte, []byte, error) {
	state, participants, err := multiGuestDoc(stateJSON)
	if err != nil {
		return nil, nil, err
	}
	at := guestIndex(participants, number)
	if at < 0 {
		return nil, nil, corei18n.User(dopestrings.Default.Games.MultiGuest.NotFound())
	}
	entered := (multi{}).EnteredSeats(json.RawMessage(stateJSON))
	if at < len(entered) && entered[at] {
		return nil, nil, corei18n.User(dopestrings.Default.Games.MultiGuest.Entered(participants[at].Name))
	}
	next := append(append([]KSIParticipant(nil), participants[:at]...), participants[at+1:]...)
	var declined map[string]bool
	if raw, ok := state["declined"]; ok && len(raw) > 0 {
		_ = json.Unmarshal(raw, &declined)
	}
	if key := KSIDeclinedKey(number, participants[at].Name); declined != nil {
		if _, ok := declined[key]; ok {
			delete(declined, key)
			raw, err := json.Marshal(declined)
			if err != nil {
				return nil, nil, err
			}
			state["declined"] = raw
		}
	}
	return rewriteMultiParticipants(schemeJSON, state, participants, next)
}

// KeepMultiGuests carries the guest teams of a Multi scheme about to be
// cleared into its pristine document: clearing wipes what was played, and a
// guest team is part of who plays.
func KeepMultiGuests(oldSchemeJSON string, schemeJSON, stateJSON []byte) ([]byte, []byte, error) {
	old, err := RawJSONObject(oldSchemeJSON)
	if err != nil {
		return nil, nil, err
	}
	guests := MultiGuests(ParseKSIParticipants(old["participants"]))
	if len(guests) == 0 {
		return schemeJSON, stateJSON, nil
	}
	state, participants, err := multiGuestDoc(string(stateJSON))
	if err != nil {
		return nil, nil, err
	}
	next := append(append([]KSIParticipant(nil), participants...), guests...)
	return rewriteMultiParticipants(string(schemeJSON), state, participants, next)
}

func multiGuestDoc(stateJSON string) (map[string]json.RawMessage, []KSIParticipant, error) {
	state, err := RawJSONObject(stateJSON)
	if err != nil {
		return nil, nil, err
	}
	return state, ParseKSIParticipants(state["participants"]), nil
}

func guestIndex(participants []KSIParticipant, number int) int {
	for i, p := range participants {
		if MultiGuest(p) && p.Number == number {
			return i
		}
	}
	return -1
}

// guestName is the name as a guest team will carry it: trimmed, and not one
// another team of this game already goes by, since the sheet, the export and
// a legacy refusal all tell teams apart by name. self is the team being renamed.
func guestName(participants []KSIParticipant, typed string, self int) (string, error) {
	name := strings.Join(strings.Fields(typed), " ")
	if name == "" {
		return "", corei18n.User(dopestrings.Default.Games.MultiGuest.NameMissing())
	}
	for _, p := range participants {
		if (self == 0 || p.Number != self) && strings.EqualFold(strings.TrimSpace(p.Name), name) {
			return "", corei18n.User(dopestrings.Default.Games.MultiGuest.NameTaken(name))
		}
	}
	return name, nil
}
