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
	lines := describeStatePatch([]byte(`{"ops":[{"op":"set","path":["sheets",0,"rows",2],"value":50}]}`))
	if len(lines) != 1 || lines[0] != "sheets · 0 · rows · 2 → 50" {
		t.Fatalf("got %#v", lines)
	}
}
