package langdetect

import (
	"os"
	"path/filepath"
	"testing"
)

// A stand-in binary that calls everything Ukrainian shows the protocol both
// ways; the real one's detection is tested in cmd/qy-langdetect.
func TestDetectRunsTheBinary(t *testing.T) {
	bin := filepath.Join(t.TempDir(), Binary)
	script := "#!/bin/sh\nsed 's/\"text\":\"[^\"]*\"/\"lang\":\"uk\"/'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := (&Runner{Path: bin}).Detect(t.Context(), map[int64]string{3: "Назвіть ЙОГО.", 5: "Що це?"})
	if err != nil || got[3] != "uk" || got[5] != "uk" || len(got) != 2 {
		t.Errorf("got %v, %v", got, err)
	}
	if _, err := (&Runner{Path: filepath.Join(t.TempDir(), "none")}).Detect(t.Context(), map[int64]string{1: "x"}); err == nil {
		t.Error("a missing binary should fail")
	}
}
