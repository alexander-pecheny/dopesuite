package tgbot

import (
	"encoding/json"
	"testing"
)

func TestARichPostFlattensToItsText(t *testing.T) {
	raw := `{"message_id":7,"rich_message":{"blocks":[
	  {"type":"paragraph","text":[{"type":"bold","text":"Question 15:"}," Some place. Name X."]},
	  {"type":"details","summary":"Answer","blocks":[
	    {"type":"paragraph","text":[{"type":"bold","text":"Answer:"}," circus"]},
	    {"type":"paragraph","text":[{"type":"bold","text":"Comment:"}," a fact."]}
	  ]},
	  {"type":"footer","text":[{"type":"bold","text":"Sources:"}," 1. ",{"type":"url","text":"https://a.example/x","url":"https://a.example/x"},"\n2. https://b.example"]},
	  {"type":"list","items":[{"label":"1.","blocks":[{"type":"paragraph","text":"first"}]}]},
	  {"type":"photo","photo":[],"caption":{"text":"a picture"}},
	  {"type":"table","cells":[[{"text":"a"},{"text":"b"}]],"caption":"t"}
	]}}`
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	want := "Question 15: Some place. Name X.\nAnswer: circus\nComment: a fact.\nSources: 1. https://a.example/x\n2. https://b.example\n1. first\na picture\na\tb\nt"
	if got := m.RichMessage.PlainText(); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}
