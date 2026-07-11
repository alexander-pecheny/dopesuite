package ui

import "testing"

func TestParse_TabsRejected(t *testing.T) {
	_, err := Parse("t.xui", []byte("doctype\nhtml\n\thead\n"))
	if err == nil {
		t.Fatal("expected an error for a tab-indented line")
	}
}

func TestParse_OddIndentRejected(t *testing.T) {
	_, err := Parse("t.xui", []byte("doctype\nhtml\n head\n"))
	if err == nil {
		t.Fatal("expected an error for an odd indentation width")
	}
}

func TestParse_IndentJumpRejected(t *testing.T) {
	_, err := Parse("t.xui", []byte("doctype\nhtml\n    head\n"))
	if err == nil {
		t.Fatal("expected an error for a two-level indentation jump")
	}
}

func TestParse_DoctypeMustBeFirst(t *testing.T) {
	_, err := Parse("t.xui", []byte("html\n  head\ndoctype\n"))
	if err == nil {
		t.Fatal("expected an error: doctype after another top-level node")
	}
}

func TestParse_DoctypeMustBeTopLevel(t *testing.T) {
	_, err := Parse("t.xui", []byte("html\n  doctype\n"))
	if err == nil {
		t.Fatal("expected an error: doctype nested inside an element")
	}
}

func TestParse_InlineAndBlockConflict(t *testing.T) {
	src := "doctype\nhtml\n  \"hi\"\n"
	// A bare inline-run line at the top isn't the case we want; build one
	// where an element line has inline items AND deeper block children.
	src = "doctype\ndiv \"hi\"\n  span\n"
	_, err := Parse("t.xui", []byte(src))
	if err == nil {
		t.Fatal("expected an error: element has both inline content and block children")
	}
}

func TestParse_SourceCommentIgnored(t *testing.T) {
	doc, err := Parse("t.xui", []byte("# not rendered\ndoctype\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("expected only the doctype node, got %d nodes", len(doc.Nodes))
	}
	if _, ok := doc.Nodes[0].(*Doctype); !ok {
		t.Fatalf("expected a *Doctype node, got %T", doc.Nodes[0])
	}
}

func TestParse_EscapesInStrings(t *testing.T) {
	doc, err := Parse("t.xui", []byte(`p "a\"b\\c\nd"`+"\n"))
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
	doc, err := Parse("t.xui", []byte(`div (span (a href="/x" "go"))`+"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	div := doc.Nodes[0].(*Element)
	span := div.Inline[0].(*Element)
	if span.Tag != "span" {
		t.Fatalf("expected span, got %s", span.Tag)
	}
	a := span.Inline[0].(*Element)
	if a.Tag != "a" || a.Attrs[0].Name != "href" || a.Attrs[0].Value != "/x" {
		t.Fatalf("nested <a> not parsed correctly: %+v", a)
	}
	text := a.Inline[0].(*TextNode)
	if text.Value != "go" {
		t.Fatalf("expected text %q, got %q", "go", text.Value)
	}
}

func TestParse_BareAndValueAttrsPreserveOrder(t *testing.T) {
	doc, err := Parse("t.xui", []byte(`input required type="text" autofocus`+"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	elem := doc.Nodes[0].(*Element)
	if len(elem.Attrs) != 3 {
		t.Fatalf("expected 3 attrs, got %d", len(elem.Attrs))
	}
	if elem.Attrs[0].Name != "required" || !elem.Attrs[0].Bare {
		t.Fatalf("attr 0 wrong: %+v", elem.Attrs[0])
	}
	if elem.Attrs[1].Name != "type" || elem.Attrs[1].Value != "text" {
		t.Fatalf("attr 1 wrong: %+v", elem.Attrs[1])
	}
	if elem.Attrs[2].Name != "autofocus" || !elem.Attrs[2].Bare {
		t.Fatalf("attr 2 wrong: %+v", elem.Attrs[2])
	}
}

func TestParse_BlankLineBubblesToCorrectDepth(t *testing.T) {
	src := "doctype\nhtml\n  head\n  body\n    header\n      div\n        a \"x\"\n\n    main\n"
	doc, err := Parse("t.xui", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	html := doc.Nodes[1].(*Element)
	body := html.Block[1].(*Element)
	if len(body.Block) != 3 {
		t.Fatalf("expected [header, blank, main] under body, got %d nodes: %+v", len(body.Block), body.Block)
	}
	if _, ok := body.Block[1].(*BlankLine); !ok {
		t.Fatalf("expected the blank line between header and main, got %T", body.Block[1])
	}
	if main, ok := body.Block[2].(*Element); !ok || main.Tag != "main" {
		t.Fatalf("expected main as body's third child, got %+v", body.Block[2])
	}
}
