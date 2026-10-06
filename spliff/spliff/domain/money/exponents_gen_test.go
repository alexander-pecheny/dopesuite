package money

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// tsExponents is the browser's copy of the exponent column. It is written from
// the Currencies table, so the two cannot drift. Regenerate with
// SPLIFF_UPDATE_EXPONENTS=1.
const tsExponents = "../../web/ts/money_exponents_gen.ts"

func renderTSExponents() string {
	var b strings.Builder
	b.WriteString("// Code generated from spliff/spliff/domain/money/currencies.go. DO NOT EDIT.\n")
	b.WriteString("// Regenerate: SPLIFF_UPDATE_EXPONENTS=1 go test ./spliff/domain/money\n\n")
	b.WriteString("// Every ISO 4217 code whose exponent is NOT 2. Any other code has two.\n")
	b.WriteString("export const EXPONENTS: Record<string, number> = {\n")
	for _, c := range Currencies {
		if c.Exponent != 2 {
			fmt.Fprintf(&b, "  %s: %d,\n", c.Code, c.Exponent)
		}
	}
	b.WriteString("};\n")
	return b.String()
}

func TestTSExponentTableIsGenerated(t *testing.T) {
	want := renderTSExponents()
	if os.Getenv("SPLIFF_UPDATE_EXPONENTS") != "" {
		if err := os.WriteFile(tsExponents, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(tsExponents)
	if err != nil {
		t.Fatalf("%v; run with SPLIFF_UPDATE_EXPONENTS=1 to write it", err)
	}
	if string(got) != want {
		t.Errorf("%s is out of date with currencies.go; rerun with SPLIFF_UPDATE_EXPONENTS=1", tsExponents)
	}
}
