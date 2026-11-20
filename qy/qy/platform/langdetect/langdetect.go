// Package langdetect runs the qy-langdetect binary (cmd/qy-langdetect) on a
// batch of questions. The binary is separate so that the server does not
// carry its language models.
package langdetect

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Binary is the detector's file name, which deploy puts next to qy-server.
const Binary = "qy-langdetect"

// Runner runs the binary at Path.
type Runner struct{ Path string }

// Find is the binary that env names, else the one next to the running
// executable. It is nil when there is none: a dev server run with go run,
// say, whose questions then wait for an admin to set their language.
func Find(env string) *Runner {
	if env != "" {
		return &Runner{Path: env}
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	p := filepath.Join(filepath.Dir(exe), Binary)
	if _, err := os.Stat(p); err != nil {
		return nil
	}
	return &Runner{Path: p}
}

type line struct {
	ID   int64  `json:"id"`
	Text string `json:"text,omitempty"`
	Lang string `json:"lang,omitempty"`
}

// Detect names the language of each text, by its key.
func (r *Runner) Detect(ctx context.Context, texts map[int64]string) (map[int64]string, error) {
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	for id, t := range texts {
		if err := enc.Encode(line{ID: id, Text: t}); err != nil {
			return nil, err
		}
	}
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, r.Path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = &in, &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", Binary, err, bytes.TrimSpace(stderr.Bytes()))
	}
	got := map[int64]string{}
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("%s: %w", Binary, err)
		}
		got[l.ID] = l.Lang
	}
	return got, sc.Err()
}
