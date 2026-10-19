package listexport

import "code.pecheny.me/pecheny/dopesuite/chgksuite/inline"

// ImportKind is the Card Kind an imported 4s block becomes (importpack.ts
// importKind). A question is recognised by its fields, not its first line:
// compose_4s puts "№ N" ahead of "? …", and unmarked text is a question whose
// author did not mark it.
func ImportKind(desc string) string {
	has := map[string]bool{}
	for _, b := range parseBlocks(desc) {
		has[b.kind] = true
	}
	switch {
	case has["theme"]:
		return "theme"
	case has["question"] || has["answer"] || has["pre"]:
		return "question"
	case has["heading"] || has["ljheading"]:
		return "heading"
	}
	return "meta"
}

// ImageRefs are the pictures desc's (img …) directives name, each once, in
// order of first use.
func ImageRefs(desc string) []string {
	var names []string
	seen := map[string]bool{}
	for _, r := range inline.Parse4sElem(desc) {
		if r.Kind != "img" {
			continue
		}
		if im, ok := inline.ParseImg(r.Text); ok && !seen[im.Name] {
			seen[im.Name] = true
			names = append(names, im.Name)
		}
	}
	return names
}
