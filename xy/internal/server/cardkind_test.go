package server

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/cardkind"
)

// The cards.kind CHECK constraint and the server's allow-list both say which
// kinds a Card may have, and cardkind is where that list lives. A kind added to
// the table needs a migration that widens the CHECK (as v24 and v27 did); this
// is the test that says so.
func TestCardKindCheckMatchesTable(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "kinds.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ddl string
	if err := db.QueryRow(`select sql from sqlite_master where type = 'table' and name = 'cards'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`check \(kind in \(([^)]*)\)\)`).FindStringSubmatch(ddl)
	if m == nil {
		t.Fatalf("no kind CHECK in the cards table:\n%s", ddl)
	}
	var checked []string
	for _, q := range strings.Split(m[1], ",") {
		checked = append(checked, strings.Trim(strings.TrimSpace(q), "'"))
	}
	want := cardkind.Names()
	slices.Sort(checked)
	slices.Sort(want)
	if !slices.Equal(checked, want) {
		t.Errorf("cards.kind CHECK allows %v, cardkind has %v", checked, want)
	}
	for _, k := range cardkind.Names() {
		if !validCardKind(k) {
			t.Errorf("validCardKind(%q) = false", k)
		}
	}
	if validCardKind("tema") {
		t.Error("validCardKind accepts a kind the table does not have")
	}
}

// The card editor's kind menu is static markup in board.dopeui; it has to
// offer the pickable kinds, in table order, and no others.
func TestKindMenuMatchesTable(t *testing.T) {
	raw, err := os.ReadFile("../../web/assets/ui/board.dopeui")
	if err != nil {
		t.Fatal(err)
	}
	menu := regexp.MustCompile(`(?s)selectfield id="cardKind"[^\n]*\n((?:\s+option [^\n]*\n)+)`).FindSubmatch(raw)
	if menu == nil {
		t.Fatal("board.dopeui: no cardKind selectfield")
	}
	var offered []string
	for _, m := range regexp.MustCompile(`option value="([^"]+)"`).FindAllSubmatch(menu[1], -1) {
		offered = append(offered, string(m[1]))
	}
	var pickable []string
	for _, k := range cardkind.All {
		if k.Pickable {
			pickable = append(pickable, k.Name)
		}
	}
	if !slices.Equal(offered, pickable) {
		t.Errorf("board.dopeui offers %v, the table's pickable kinds are %v", offered, pickable)
	}
}
