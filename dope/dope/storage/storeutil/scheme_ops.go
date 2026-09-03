package storeutil

import (
	"strconv"
	"strings"

	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// ValidateScheme checks that a parsed fest scheme is internally consistent
// before it is materialised into the database (unique stage/match codes, valid
// seed slots, non-colliding team basket/number assignments). It is a pure
// validation over the scheme data shapes and carries no DB/server dependency.
// Every failure is one a host importing the scheme may read, so each is a
// Catalog string wrapped as a UserError the edge shows verbatim
// (root docs/adr/0006).
func ValidateScheme(scheme store.FestScheme) error {
	if strings.TrimSpace(scheme.Slug) == "" {
		return corei18n.User(dopestrings.Default.Scheme.Validate.SlugRequired())
	}
	if strings.TrimSpace(scheme.Title) == "" {
		return corei18n.User(dopestrings.Default.Scheme.Validate.TitleRequired())
	}
	gameType := scheme.GameType
	// EK is the default game type when none is recorded.
	if (gameType == "" || gameType == "ek") && len(scheme.Stages) == 0 {
		return corei18n.User(dopestrings.Default.Scheme.Validate.StagesRequired())
	}
	stageCodes := make(map[string]struct{}, len(scheme.Stages))
	matchCodes := make(map[string]struct{})
	for _, stage := range scheme.Stages {
		if strings.TrimSpace(stage.Code) == "" {
			return corei18n.User(dopestrings.Default.Scheme.Validate.StageCodeRequired())
		}
		if _, exists := stageCodes[stage.Code]; exists {
			return corei18n.User(dopestrings.Default.Scheme.Validate.StageCodeDup(stage.Code))
		}
		stageCodes[stage.Code] = struct{}{}
		stageType := stage.StageType
		if stageType == "" {
			stageType = "matches"
		}
		if stageType != "matches" && stageType != "reseed" {
			return corei18n.User(dopestrings.Default.Scheme.Validate.StageType(stage.StageType))
		}
		if stageType == "matches" && len(stage.Matches) == 0 {
			return corei18n.User(dopestrings.Default.Scheme.Validate.StageNoMatches(stage.Code))
		}
		for _, match := range stage.Matches {
			if strings.TrimSpace(match.Code) == "" {
				return corei18n.User(dopestrings.Default.Scheme.Validate.MatchCodeRequired(stage.Code))
			}
			if _, exists := matchCodes[match.Code]; exists {
				return corei18n.User(dopestrings.Default.Scheme.Validate.MatchCodeDup(match.Code))
			}
			matchCodes[match.Code] = struct{}{}
			if match.ParticipantCount > 0 && len(match.Slots) != match.ParticipantCount {
				return corei18n.User(dopestrings.Default.Scheme.Validate.SlotCount(match.Code))
			}
			for slotIndex, slot := range match.Slots {
				if slot.Team != nil {
					return corei18n.User(dopestrings.Default.Scheme.Validate.SlotTeamSource(match.Code, strconv.Itoa(slotIndex)))
				}
				if slot.Seed == nil {
					continue
				}
				number := slot.Seed.Number
				if number == 0 {
					number = slot.Seed.Position
				}
				if number <= 0 {
					return corei18n.User(dopestrings.Default.Scheme.Validate.SlotSeedNumber(match.Code, strconv.Itoa(slotIndex)))
				}
				if slot.Seed.Basket < 0 {
					return corei18n.User(dopestrings.Default.Scheme.Validate.SlotSeedBasket(match.Code, strconv.Itoa(slotIndex)))
				}
			}
		}
	}
	assignmentKeys := make(map[[2]int]string, len(scheme.Teams))
	for index, team := range scheme.Teams {
		if strings.TrimSpace(team.Name) == "" {
			return corei18n.User(dopestrings.Default.Scheme.Validate.TeamNameRequired(strconv.Itoa(index)))
		}
		if team.Basket <= 0 || team.Number <= 0 {
			return corei18n.User(dopestrings.Default.Scheme.Validate.TeamAssignment(strconv.Itoa(index), team.Name))
		}
		key := [2]int{team.Basket, team.Number}
		if existing, ok := assignmentKeys[key]; ok {
			return corei18n.User(dopestrings.Default.Scheme.Validate.TeamCollision(strconv.Itoa(index), team.Name, strconv.Itoa(team.Basket), strconv.Itoa(team.Number), existing))
		}
		assignmentKeys[key] = team.Name
	}
	return nil
}
