package ui

import "testing"

func TestParse_TabsRejected(t *testing.T) {
	_, err := Parse("t.dopeui", []byte("page\n\ttopbar\n"))
	if err == nil {
		t.Fatal("expected an error for a tab-indented line")
	}
}

func TestParse_OddIndentRejected(t *testing.T) {
	_, err := Parse("t.dopeui", []byte("page\n topbar\n"))
	if err == nil {
		t.Fatal("expected an error for an odd indentation width")
	}
}

func TestParse_IndentJumpRejected(t *testing.T) {
	_, err := Parse("t.dopeui", []byte("page\n    topbar\n"))
	if err == nil {
		t.Fatal("expected an error for a two-level indentation jump")
	}
}

func TestParse_InlineAndBlockConflict(t *testing.T) {
	_, err := Parse("t.dopeui", []byte("row \"hi\"\n  col\n"))
	if err == nil {
		t.Fatal("expected an error: element has both inline content and block children")
	}
}

func TestParse_SourceCommentIgnored(t *testing.T) {
	doc, err := Parse("t.dopeui", []byte("# not rendered\nspacer\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("expected only the spacer node, got %d nodes", len(doc.Nodes))
	}
	if e, ok := doc.Nodes[0].(*Element); !ok || e.Tag != "spacer" {
		t.Fatalf("expected a spacer element, got %#v", doc.Nodes[0])
	}
}

func TestParse_EscapesInStrings(t *testing.T) {
	doc, err := Parse("t.dopeui", []byte(`text "a\"b\\c\nd"`+"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	elem := doc.Nodes[0].(*Element)
	text := elem.Inline[0].(*TextNode)
	want := "a\"b\\c\nd"
	if text.Value != want {
		t.Fatalf("escape decode mismatch: got %q, want %q", text.Value, want)
	}
}

func TestParse_NestedParens(t *testing.T) {
	doc, err := Parse("t.dopeui", []byte(`hint (strong (code "go"))`+"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	hint := doc.Nodes[0].(*Element)
	strong := hint.Inline[0].(*Element)
	if strong.Tag != "strong" {
		t.Fatalf("expected strong, got %s", strong.Tag)
	}
	code := strong.Inline[0].(*Element)
	if code.Tag != "code" {
		t.Fatalf("expected code, got %s", code.Tag)
	}
	if text := code.Inline[0].(*TextNode); text.Value != "go" {
		t.Fatalf("expected text %q, got %q", "go", text.Value)
	}
}

func TestParse_BareAndValueAttrsPreserveOrder(t *testing.T) {
	doc, err := Parse("t.dopeui", []byte(`textfield required autofocus name="u"`+"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	elem := doc.Nodes[0].(*Element)
	if len(elem.Attrs) != 3 {
		t.Fatalf("expected 3 props, got %d", len(elem.Attrs))
	}
	if elem.Attrs[0].Name != "required" || !elem.Attrs[0].Bare {
		t.Fatalf("prop 0 wrong: %+v", elem.Attrs[0])
	}
	if elem.Attrs[2].Name != "name" || elem.Attrs[2].Value != "u" {
		t.Fatalf("prop 2 wrong: %+v", elem.Attrs[2])
	}
}

func TestParse_BlankLineBubblesToCorrectDepth(t *testing.T) {
	src := "page\n  section\n    hint\n      \"x\"\n\n  section\n"
	doc, err := Parse("t.dopeui", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	page := doc.Nodes[0].(*Element)
	if len(page.Block) != 3 {
		t.Fatalf("expected [section, blank, section] under page, got %d nodes", len(page.Block))
	}
	if _, ok := page.Block[1].(*BlankLine); !ok {
		t.Fatalf("expected a blank line between the sections, got %T", page.Block[1])
	}
}
