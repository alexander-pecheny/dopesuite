package tg

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PollConfig is chgksuite's poll_config.toml: whether the polls go under the
// posts as comments or into the channel, and one poll each after a question, a
// tour and the whole package.
type PollConfig struct {
	// Mode is "comment" (a reply in the discussion group) or "channel".
	Mode                   string
	Question, Tour, Packet *Poll
}

// Poll is one configured poll. {NUMBER} and {TITLE} in Text are filled in.
type Poll struct {
	Text              string
	Variants          []string
	IsAnonymous       bool
	AllowsRevoting    bool
	AllowsRevotingSet bool
	QuizRightAnswer   string
}

// ParsePollConfig reads poll_config.toml. It is not a TOML parser: it reads the
// shape chgksuite ships and nothing else — a top-level `mode`, then a table per
// poll with a string, a list of strings and a few booleans. Anything else is an
// error, which beats half-reading a file someone hand-wrote.
func ParsePollConfig(src string) (*PollConfig, error) {
	cfg := &PollConfig{Mode: "comment"}
	polls := map[string]*Poll{}
	var cur *Poll
	for n, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			name := strings.Trim(line, "[]")
			if name != "question_poll" && name != "tour_poll" && name != "packet_poll" {
				return nil, fmt.Errorf("line %d: unknown table [%s]", n+1, name)
			}
			cur = &Poll{}
			polls[name] = cur
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", n+1)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(stripComment(value))
		if cur == nil {
			if key != "mode" {
				return nil, fmt.Errorf("line %d: %s belongs to a poll table", n+1, key)
			}
			s, err := tomlString(value)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n+1, err)
			}
			cfg.Mode = s
			continue
		}
		if err := setPollField(cur, key, value); err != nil {
			return nil, fmt.Errorf("line %d: %w", n+1, err)
		}
	}
	cfg.Question, cfg.Tour, cfg.Packet = polls["question_poll"], polls["tour_poll"], polls["packet_poll"]
	return cfg, nil
}

func setPollField(p *Poll, key, value string) error {
	switch key {
	case "text", "quiz_right_answer":
		s, err := tomlString(value)
		if err != nil {
			return err
		}
		if key == "text" {
			p.Text = s
		} else {
			p.QuizRightAnswer = s
		}
	case "variants":
		list, err := tomlStringList(value)
		if err != nil {
			return err
		}
		p.Variants = list
	case "is_anonymous", "allows_revoting":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if key == "is_anonymous" {
			p.IsAnonymous = b
		} else {
			p.AllowsRevoting, p.AllowsRevotingSet = b, true
		}
	default:
		return fmt.Errorf("unknown key %s", key)
	}
	return nil
}

// stripComment drops a trailing # comment, unless it is inside the value.
func stripComment(s string) string {
	inQuotes := false
	for i, r := range s {
		switch r {
		case '"':
			inQuotes = !inQuotes
		case '#':
			if !inQuotes {
				return s[:i]
			}
		}
	}
	return s
}

// tomlString reads a basic TOML string, whose escapes are JSON's.
func tomlString(s string) (string, error) {
	var out string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return "", fmt.Errorf("not a string: %s", s)
	}
	return out, nil
}

func tomlStringList(s string) ([]string, error) {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("not a list of strings: %s", s)
	}
	return out, nil
}

// jsonArray is how sendPoll wants its options: a JSON array, as a string. It is
// written the way python's json.dumps writes it by default — every non-ASCII
// rune escaped, a space after each comma — so the request is chgksuite's own,
// byte for byte.
func jsonArray(list []string) (string, error) {
	parts := make([]string, 0, len(list))
	for _, s := range list {
		b, err := json.Marshal(s)
		if err != nil {
			return "", err
		}
		parts = append(parts, asciiEscape(string(b)))
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
}

// asciiEscape rewrites every rune above ASCII as \uXXXX, in surrogate pairs
// where python would (json.dumps with ensure_ascii, its default).
func asciiEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r > 0xFFFF:
			r -= 0x10000
			fmt.Fprintf(&b, "\\u%04x\\u%04x", 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		default:
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	return b.String()
}
