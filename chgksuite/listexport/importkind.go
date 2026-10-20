package listexport

import (
	"code.pecheny.me/pecheny/dopesuite/chgksuite/cardkind"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/inline"
)

// ImportKind is the Card Kind an imported 4s block becomes (importpack.ts
// importKind). A question is recognised by its fields, not its first line:
// compose_4s puts "№ N" ahead of "? …", and unmarked text is a question whose
// author did not mark it. A theme is the kind of theme the game writes.
func ImportKind(desc, game string) string {
	has := map[string]bool{}
	for _, b := range parseBlocks(desc) {
		has[b.kind] = true
	}
	switch {
	case has["theme"] && game == cardkind.GameTroika:
		return cardkind.TroikaTheme
	case has["theme"]:
		return cardkind.Theme
	case has["question"] || has["answer"] || has["pre"]:
		return cardkind.Question
	case has["battle"]:
		return cardkind.Battle
	case has["section"] || has["heading"] || has["ljheading"]:
		return cardkind.Heading
	}
	return cardkind.Meta
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
