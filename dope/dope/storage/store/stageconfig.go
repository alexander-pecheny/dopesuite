package store

import (
	"encoding/json"
	"strings"
)

// StageConfig is stages.config_json: what the scheme says about a Block around
// its Kind's own config — the reseed's sources, bands and sort, a layout — with
// the Kind's (and the Protocol's) settings nested under Config. One type on
// both sides of the column, so the readers stop guessing the nesting.
type StageConfig struct {
	Teams   []SchemeSlot    `json:"teams,omitempty"`
	Bands   []int           `json:"bands,omitempty"`
	Sources []string        `json:"sources,omitempty"`
	Sort    json.RawMessage `json:"sort,omitempty"`
	Config  json.RawMessage `json:"config,omitempty"`
	Layout  json.RawMessage `json:"layout,omitempty"`
	// Questions at the top level is the legacy spelling of Config.questions.
	LegacyQuestions int `json:"questions,omitempty"`

	raw []byte // the stored bytes, when parsed from a row
}

// StageConfigOf is the envelope a scheme stage writes.
func StageConfigOf(stage SchemeStage) StageConfig {
	c := StageConfig{Teams: stage.Teams, Bands: stage.Bands, Sources: stage.Sources}
	if len(stage.Sort) > 0 {
		c.Sort = stage.Sort
	}
	if len(stage.Config) > 0 {
		c.Config = stage.Config
	}
	if len(stage.Layout) > 0 {
		c.Layout = stage.Layout
	}
	return c
}

// ParseStageConfig reads a stored envelope; anything unreadable is empty.
func ParseStageConfig(raw string) StageConfig {
	var c StageConfig
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &c)
		c.raw = []byte(raw)
	}
	return c
}

// JSON is the stored form: only what is set, compact.
func (c StageConfig) JSON() string {
	b, _ := json.Marshal(c)
	return string(b)
}

// KindConfig is the config a Kind reads: the nested Config when the scheme
// wrote one, else the envelope itself for a Kind whose settings sit at the top
// (a reseed's teams, bands and sort).
func (c StageConfig) KindConfig() json.RawMessage {
	if len(c.Config) > 0 && string(c.Config) != "null" {
		return c.Config
	}
	if len(c.raw) > 0 {
		return c.raw
	}
	b, _ := json.Marshal(c)
	return b
}

// protocolConfig is what a Protocol keeps under Config: the per-бой question
// count and the КСИ/ЭК theme count.
type protocolConfig struct {
	Questions int `json:"questions"`
	Themes    int `json:"themes"`
}

func (c StageConfig) protocol() protocolConfig {
	var p protocolConfig
	if len(c.Config) > 0 {
		_ = json.Unmarshal(c.Config, &p)
	}
	return p
}

// Questions is the Block's per-бой question count: Config.questions, else the
// legacy top-level one, else 0.
func (c StageConfig) Questions() int {
	if q := c.protocol().Questions; q > 0 {
		return q
	}
	return c.LegacyQuestions
}

// Themes is how many themes the Block's бои play; 0 means the Protocol's default.
func (c StageConfig) Themes() int { return c.protocol().Themes }
