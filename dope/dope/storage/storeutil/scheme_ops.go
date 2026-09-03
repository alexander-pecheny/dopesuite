package storeutil

import (
	"errors"
	"fmt"
	"strings"

	"dope/dope/storage/store"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// ValidateScheme checks that a parsed fest scheme is internally consistent
// before it is materialised into the database (unique stage/match codes, valid
// seed slots, non-colliding team basket/number assignments). It is a pure
// validation over the scheme data shapes and carries no DB/server dependency.
// Every failure is one a host importing the scheme may read, so they leave as
// UserErrors the edge shows verbatim (root docs/adr/0006).
func ValidateScheme(scheme store.FestScheme) error {
	if err := validateScheme(scheme); err != nil {
		return corei18n.User(err.Error())
	}
	return nil
}

func validateScheme(scheme store.FestScheme) error {
	if strings.TrimSpace(scheme.Slug) == "" {
		return errors.New("schema slug is required")
	}
	if strings.TrimSpace(scheme.Title) == "" {
		return errors.New("schema title is required")
	}
	gameType := scheme.GameType
	// EK is the default game type when none is recorded.
	if (gameType == "" || gameType == "ek") && len(scheme.Stages) == 0 {
		return errors.New("schema stages are required")
	}
	stageCodes := make(map[string]struct{}, len(scheme.Stages))
	matchCodes := make(map[string]struct{})
	for _, stage := range scheme.Stages {
		if strings.TrimSpace(stage.Code) == "" {
			return errors.New("stage code is required")
		}
		if _, exists := stageCodes[stage.Code]; exists {
			return fmt.Errorf("duplicate stage code %q", stage.Code)
		}
		stageCodes[stage.Code] = struct{}{}
		stageType := stage.StageType
		if stageType == "" {
			stageType = "matches"
		}
		if stageType != "matches" && stageType != "reseed" {
			return fmt.Errorf("bad stage_type %q", stage.StageType)
		}
		if stageType == "matches" && len(stage.Matches) == 0 {
			return fmt.Errorf("stage %q has no matches", stage.Code)
		}
		for _, match := range stage.Matches {
			if strings.TrimSpace(match.Code) == "" {
				return fmt.Errorf("match code is required in stage %q", stage.Code)
			}
			if _, exists := matchCodes[match.Code]; exists {
				return fmt.Errorf("duplicate match code %q", match.Code)
			}
			matchCodes[match.Code] = struct{}{}
			if match.ParticipantCount > 0 && len(match.Slots) != match.ParticipantCount {
				return fmt.Errorf("match %q participantCount does not match slots", match.Code)
			}
			for slotIndex, slot := range match.Slots {
				if slot.Team != nil {
					return fmt.Errorf("match %q slot %d uses removed source %q; use seed-N or seed{basket,number}; teams come from separate seed import", match.Code, slotIndex, "team")
				}
				if slot.Seed != nil {
					number := slot.Seed.Number
					if number == 0 {
						number = slot.Seed.Position
					}
					if number <= 0 {
						return fmt.Errorf("match %q slot %d has bad seed number", match.Code, slotIndex)
					}
					if slot.Seed.Basket < 0 {
						return fmt.Errorf("match %q slot %d has bad seed basket", match.Code, slotIndex)
					}
				}
			}
		}
	}
	assignmentKeys := make(map[[2]int]string, len(scheme.Teams))
	for index, team := range scheme.Teams {
		if strings.TrimSpace(team.Name) == "" {
			return fmt.Errorf("teams[%d].name is required", index)
		}
		if team.Basket <= 0 || team.Number <= 0 {
			return fmt.Errorf("teams[%d] (%q) must have basket>=1 and number>=1", index, team.Name)
		}
		key := [2]int{team.Basket, team.Number}
		if existing, ok := assignmentKeys[key]; ok {
			return fmt.Errorf("teams[%d] (%q) collides with %q on basket %d / number %d", index, team.Name, existing, team.Basket, team.Number)
		}
		assignmentKeys[key] = team.Name
	}
	return nil
}
