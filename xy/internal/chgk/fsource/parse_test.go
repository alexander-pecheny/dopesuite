package fsource

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParseParity parses each testdata/*.4s and compares the structure (modulo
// JSON key order) to chgksuite's own `--debug` dump in the matching *.dbg.json.
// Regenerate oracles with: chgksuite --debug compose docx <file> (writes <name>.dbg).
func TestParseParity(t *testing.T) {
	files, err := filepath.Glob("testdata/*.4s")
	if err != nil || len(files) == 0 {
		t.Fatalf("no testdata: %v", err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".4s")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			oracle, err := os.ReadFile(filepath.Join("testdata", name+".dbg.json"))
			if err != nil || len(bytes.TrimSpace(oracle)) == 0 {
				t.Skipf("no oracle for %s", name)
			}
			mine, err := json.Marshal(Parse(string(src), "chgk"))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var want, got any
			if err := json.Unmarshal(oracle, &want); err != nil {
				t.Fatalf("oracle json: %v", err)
			}
			if err := json.Unmarshal(mine, &got); err != nil {
				t.Fatalf("mine json: %v", err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("structure mismatch for %s\n--- chgksuite ---\n%s\n--- go ---\n%s",
					name, reindent(oracle), reindent(mine))
			}
		})
	}
}

func reindent(b []byte) string {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return string(b)
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	return string(out)
}
