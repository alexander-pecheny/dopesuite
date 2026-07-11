package ui

import "testing"

func TestValidate_UnknownElement(t *testing.T) {
	doc, err := Parse("t.xui", []byte("doctype\nfoo\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Validate("t.xui", doc); err == nil {
		t.Fatal("expected an error for an unknown element")
	}
}

func TestValidate_UnknownAttr(t *testing.T) {
	doc, err := Parse("t.xui", []byte(`div bogus="x"`+"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Validate("t.xui", doc); err == nil {
		t.Fatal("expected an error for an unknown attribute")
	}
}

func TestValidate_UnknownClassToken(t *testing.T) {
	doc, err := Parse("t.xui", []byte(`div class="not-a-real-token"`+"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Validate("t.xui", doc); err == nil {
		t.Fatal("expected an error for an unknown class token")
	}
}

func TestValidate_DuplicateID(t *testing.T) {
	src := "doctype\ndiv\n  span id=\"x\"\n  span id=\"x\"\n"
	doc, err := Parse("t.xui", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Validate("t.xui", doc); err == nil {
		t.Fatal("expected an error for a duplicate id")
	}
}

func TestValidate_VoidElementWithChildren(t *testing.T) {
	src := "doctype\nmeta charset=\"utf-8\"\n  div\n"
	doc, err := Parse("t.xui", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Validate("t.xui", doc); err == nil {
		t.Fatal("expected an error: void element with block children")
	}
}

func TestValidate_ValidPageOK(t *testing.T) {
	src := `doctype
html lang="ru"
  head
    meta charset="utf-8"
    title "Т"
  body class="host"
    header class="host-top"
      h1 "Х"
`
	doc, err := Parse("t.xui", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := Validate("t.xui", doc); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_ErrorHasFileAndLine(t *testing.T) {
	doc, err := Parse("mypage.xui", []byte("doctype\nfoo\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = Validate("mypage.xui", doc)
	if err == nil {
		t.Fatal("expected an error")
	}
	uiErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *ui.Error, got %T", err)
	}
	if uiErr.File != "mypage.xui" || uiErr.Line != 2 {
		t.Fatalf("expected mypage.xui:2, got %s:%d", uiErr.File, uiErr.Line)
	}
}
