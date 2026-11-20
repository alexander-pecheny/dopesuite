// Package translate asks a language model to put a question that is not in
// Russian into Russian for the jury. It translates the anonymised copy, so the
// redactions made in the original carry over; an admin edits the result.
package translate

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strings"

	"qy/qy/domain/question"
)

// Fields are the ones translated. The sources are titles and links, which the
// jury follows as they are, and the credits never reach the jury.
var Fields = []string{
	question.FieldText, question.FieldAnswer, question.FieldZachet,
	question.FieldNezachet, question.FieldComment,
}

//go:embed prompt.md
var promptTemplate string

// Prompt is the instruction and the material for a model that translates q,
// which answers with the JSON ParseReply reads. language names the language q
// is written in, in the prepositional case the prompt's sentence wants.
func Prompt(q question.Question, language string) (system, user string) {
	system = strings.ReplaceAll(promptTemplate, "{{language}}", language)
	fields := map[string]string{}
	for _, f := range Fields {
		if v := q.Get(f); v != "" {
			fields[f] = v
		}
	}
	material, _ := json.MarshalIndent(fields, "", "  ")
	return system, string(material)
}

// ParseReply reads a model's translation of q. A field the model left out or
// blanked is an error, not a silent gap; a field q does not have stays empty
// whatever the model says. The fields not translated are copied from q.
func ParseReply(reply string, q question.Question) (question.Question, error) {
	start, end := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if start < 0 || end < start {
		return question.Question{}, errors.New("no JSON object in the reply")
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(reply[start:end+1]), &got); err != nil {
		return question.Question{}, err
	}
	out := q
	for _, f := range Fields {
		if q.Get(f) == "" {
			continue
		}
		v := strings.TrimSpace(got[f])
		if v == "" {
			return question.Question{}, errors.New("the reply has no " + f)
		}
		*out.Ptr(f) = v
	}
	return out, nil
}
