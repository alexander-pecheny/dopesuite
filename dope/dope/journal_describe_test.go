package main

import "testing"

func TestDescribeMatchUpdate(t *testing.T) {
	lines := describeMatchUpdate([]byte(`[{"team":2,"theme":0,"answer":4,"mark":"right"}]`))
	if len(lines) != 1 || lines[0] != "тема 1, вопрос 5, команда 3: верно" {
		t.Fatalf("got %#v", lines)
	}
	fin := describeMatchUpdate([]byte(`[{"team":0,"finished":true}]`))
	if len(fin) != 1 || fin[0] != "матч завершён" {
		t.Fatalf("finish got %#v", fin)
	}
	pl := describeMatchUpdate([]byte(`[{"team":1,"place":2}]`))
	if pl[0] != "команда 2: место 2" {
		t.Fatalf("place got %#v", pl)
	}
}

func TestDescribeStatePatch(t *testing.T) {
	// OD/КВРМ answer grid: themes[t].answers[q][team] = mark.
	lines := describeStatePatch([]byte(`{"ops":[{"op":"set","path":["themes",3,"answers",13,1],"value":"wrong"}]}`))
	if len(lines) != 1 || lines[0] != "тема 4, вопрос 14, команда 2: неверно" {
		t.Fatalf("got %#v", lines)
	}
	// Non-mark scalar falls back to "path → value".
	score := describeStatePatch([]byte(`{"ops":[{"op":"set","path":["teams",0,"score"],"value":50}]}`))
	if score[0] != "команда 1, score → 50" {
		t.Fatalf("score got %#v", score)
	}
}
