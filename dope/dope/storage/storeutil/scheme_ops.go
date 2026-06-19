package storeutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"dope/dope/platform/util"
	"dope/dope/storage/store"
)

// ValidateScheme checks that a parsed fest scheme is internally consistent
// before it is materialised into the database (unique stage/match codes, valid
// seed slots, non-colliding team basket/number assignments). It is a pure
// validation over the scheme data shapes and carries no DB/server dependency.
func ValidateScheme(scheme store.FestScheme) error {
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

// StageConfigJSON serialises the optional, type-specific configuration of a
// scheme stage (teams, sources, sort, config, layout) into the compact JSON
// stored in stages.config_json.
func StageConfigJSON(stage store.SchemeStage) string {
	config := map[string]json.RawMessage{}
	if len(stage.Teams) > 0 {
		data, _ := json.Marshal(stage.Teams)
		config["teams"] = data
	}
	if len(stage.Sources) > 0 {
		data, _ := json.Marshal(stage.Sources)
		config["sources"] = data
	}
	if len(stage.Sort) > 0 {
		config["sort"] = stage.Sort
	}
	if len(stage.Config) > 0 {
		config["config"] = stage.Config
	}
	if len(stage.Layout) > 0 {
		config["layout"] = stage.Layout
	}
	return util.MustJSON(config)
}

// SlotSource derives the (source_type, source_ref_json) pair persisted for a
// match slot from its scheme description (seed, from_match, reseed or a bare
// placeholder/label).
func SlotSource(slot store.SchemeSlot) (string, string) {
	if slot.Seed != nil {
		number := slot.Seed.Number
		if number == 0 {
			number = slot.Seed.Position
		}
		basket := slot.Seed.Basket
		if basket <= 0 {
			basket = 1
		}
		label := slot.Label
		if label == "" && slot.Seed.Basket <= 0 {
			label = fmt.Sprintf("Посев-%d", number)
		}
		return "seed", util.MustJSON(map[string]any{
			"basket": basket,
			"number": number,
			"label":  label,
		})
	}
	if slot.FromMatch != nil {
		return "from_match", util.MustJSON(map[string]any{
			"match": slot.FromMatch.Match,
			"place": slot.FromMatch.Place,
			"label": slot.Label,
		})
	}
	if slot.Reseed != nil {
		return "reseed", util.MustJSON(map[string]any{
			"stage": slot.Reseed.Stage,
			"rank":  slot.Reseed.Rank,
			"label": slot.Label,
		})
	}
	if slot.Placeholder != "" {
		return "placeholder", util.MustJSON(map[string]string{
			"placeholder": slot.Placeholder,
			"label":       slot.Label,
		})
	}
	if slot.Label != "" {
		return "placeholder", util.MustJSON(map[string]string{"label": slot.Label})
	}
	return "placeholder", "{}"
}
