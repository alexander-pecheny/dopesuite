package kit

import "testing"

func mustParse(t *testing.T, src string) *Doc {
	t.Helper()
	doc, err := Parse("t.dopeui", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

func wantInvalid(t *testing.T, src string) {
	t.Helper()
	if err := Validate(CoreVocab, "t.dopeui", mustParse(t, src)); err == nil {
		t.Fatalf("expected a validation error for:\n%s", src)
	}
}

func TestValidate_UnknownPrimitive(t *testing.T)       { wantInvalid(t, "frobnicate\n") }
func TestValidate_UnknownProp(t *testing.T)            { wantInvalid(t, `row bogus="x"`+"\n") }
func TestValidate_InvalidEnumValue(t *testing.T)       { wantInvalid(t, `row gap="huge"`+"\n") }
func TestValidate_MissingRequiredProp(t *testing.T)    { wantInvalid(t, `mount id="x"`+"\n") }
func TestValidate_DuplicateID(t *testing.T)            { wantInvalid(t, "col\n  row id=\"a\"\n  row id=\"a\"\n") }
func TestValidate_ChildNotInNamedSet(t *testing.T)     { wantInvalid(t, "tabs\n  row\n") }
func TestValidate_TextPolicyRejectsBlock(t *testing.T) { wantInvalid(t, "hint\n  row\n") }
func TestValidate_NoneChildrenRejected(t *testing.T)   { wantInvalid(t, `spacer "x"`+"\n") }

func TestValidate_BareVsValueMismatch(t *testing.T) {
	wantInvalid(t, `textfield required="x"`+"\n")
	wantInvalid(t, `row gap`+"\n")
}

func TestValidate_ContentCellAllowsBlock(t *testing.T) {
	src := "table\n  trow\n    cell\n      row\n"
	if err := Validate(CoreVocab, "t.dopeui", mustParse(t, src)); err != nil {
		t.Fatalf("a content-policy cell should accept a block child: %v", err)
	}
}

func TestValidate_PatternPropsAllowed(t *testing.T) {
	if err := Validate(CoreVocab, "t.dopeui", mustParse(t, `row data-view="x" aria-label="y"`+"\n")); err != nil {
		t.Fatalf("data-*/aria-* props should validate: %v", err)
	}
}

func TestValidate_ErrorHasFileAndLine(t *testing.T) {
	doc := mustParse(t, "row\nfrobnicate\n")
	err := Validate(CoreVocab, "mypage.dopeui", doc)
	uiErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *ui.Error, got %T (%v)", err, err)
	}
	if uiErr.File != "mypage.dopeui" || uiErr.Line != 2 {
		t.Fatalf("expected mypage.dopeui:2, got %s:%d", uiErr.File, uiErr.Line)
	}
}
