package ui

import "testing"

func mustParse(t *testing.T, src string) *Doc {
	t.Helper()
	doc, err := Parse("t.xui", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

func wantInvalid(t *testing.T, src string) {
	t.Helper()
	if err := Validate("t.xui", mustParse(t, src)); err == nil {
		t.Fatalf("expected a validation error for:\n%s", src)
	}
}

func TestValidate_UnknownPrimitive(t *testing.T) {
	wantInvalid(t, "frobnicate\n")
}

func TestValidate_UnknownProp(t *testing.T) {
	wantInvalid(t, `row bogus="x"`+"\n")
}

func TestValidate_InvalidEnumValue(t *testing.T) {
	wantInvalid(t, `row gap="huge"`+"\n")
}

func TestValidate_MissingRequiredProp(t *testing.T) {
	wantInvalid(t, `mount id="x"`+"\n") // mount requires kind
}

func TestValidate_DuplicateID(t *testing.T) {
	wantInvalid(t, "col\n  row id=\"a\"\n  row id=\"a\"\n")
}

func TestValidate_ChildNotInNamedSet(t *testing.T) {
	wantInvalid(t, "tabs\n  row\n") // tabs allows only tab
}

func TestValidate_TextPolicyRejectsBlock(t *testing.T) {
	wantInvalid(t, "hint\n  row\n") // hint takes text, not a container
}

func TestValidate_NoneChildrenRejected(t *testing.T) {
	wantInvalid(t, `spacer "x"`+"\n") // spacer takes no children
}

func TestValidate_BareVsValueMismatch(t *testing.T) {
	wantInvalid(t, `textfield required="x"`+"\n") // required is a flag
	wantInvalid(t, `row gap`+"\n")                // gap needs a value
}

func TestValidate_PatternPropsAllowed(t *testing.T) {
	if err := Validate("t.xui", mustParse(t, `row data-view="x" aria-label="y"`+"\n")); err != nil {
		t.Fatalf("data-*/aria-* props should validate: %v", err)
	}
}

func TestValidate_ValidTreeOK(t *testing.T) {
	src := "col gap=\"md\"\n  hint \"Привет\"\n  row justify=\"between\"\n    button \"OK\"\n"
	if err := Validate("t.xui", mustParse(t, src)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_ErrorHasFileAndLine(t *testing.T) {
	doc := mustParse(t, "row\nfrobnicate\n")
	err := Validate("mypage.xui", doc)
	uiErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *ui.Error, got %T (%v)", err, err)
	}
	if uiErr.File != "mypage.xui" || uiErr.Line != 2 {
		t.Fatalf("expected mypage.xui:2, got %s:%d", uiErr.File, uiErr.Line)
	}
}
