package ui

// Compile parses, validates and renders a .xui source file. name labels
// errors as "name:line: message".
func Compile(name string, src []byte) ([]byte, error) {
	doc, err := Parse(name, src)
	if err != nil {
		return nil, err
	}
	if err := Validate(name, doc); err != nil {
		return nil, err
	}
	return render(doc), nil
}
