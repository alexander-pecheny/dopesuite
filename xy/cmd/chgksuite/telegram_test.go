package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"xy/internal/chgk/fsource"
)

const (
	packetWithStats    = "? Вопрос?\n! Ответ\n/ Взятия: 3/10\n"
	packetWithoutStats = "? Вопрос?\n! Ответ\n/ Комментарий\n"
)

// TestHasStatsSaysWhatThePacketCarries is the answer chgksuite-gui reads
// before a telegram export.
func TestHasStatsSaysWhatThePacketCarries(t *testing.T) {
	for src, want := range map[string]string{
		packetWithStats:    `{"has_stats":true}` + "\n",
		packetWithoutStats: `{"has_stats":false}` + "\n",
	} {
		path := filepath.Join(t.TempDir(), "packet.4s")
		if err := os.WriteFile(path, []byte(src), outputFileMode); err != nil {
			t.Fatal(err)
		}
		got, err := hasStatsJSON(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%q → %s, want %s", src, got, want)
		}
	}
}

// TestAllowNoStatsLiftsTheStop: stop_if_no_stats refuses a packet without
// stats unless allow_no_stats says the person agreed to it.
func TestAllowNoStatsLiftsTheStop(t *testing.T) {
	bare := fsource.Parse(packetWithoutStats, "chgk")
	withStats := fsource.Parse(packetWithStats, "chgk")
	if statsGate(bare, true, false) == nil {
		t.Error("stop_if_no_stats let a packet without stats through")
	}
	if err := statsGate(bare, true, true); err != nil {
		t.Errorf("allow_no_stats did not lift the stop: %v", err)
	}
	if err := statsGate(bare, false, false); err != nil {
		t.Errorf("refused without stop_if_no_stats: %v", err)
	}
	if err := statsGate(withStats, true, false); err != nil {
		t.Errorf("refused a packet with stats: %v", err)
	}
}

// TestOnlyTheTelegramExportIsConfirmed: the spec tells chgksuite-gui to check
// for stats before compose telegram, with the check and the flag this CLI
// has, and asks about no other command.
func TestOnlyTheTelegramExportIsConfirmed(t *testing.T) {
	for _, r := range runnables {
		c := confirms(r.verb)
		if r.verb != "compose telegram" {
			if c != nil {
				t.Errorf("%s asks before running", r.verb)
			}
			continue
		}
		if c == nil || !slices.Equal(c.Check, []string{"compose", "has_stats"}) ||
			c.Field != "has_stats" || c.Flag != "allow_no_stats" ||
			!slices.Equal(c.SkipIf, []string{"dry_run"}) || c.Question == "" {
			t.Fatalf("compose telegram confirm: %+v", c)
		}
		flags, err := flagsOf(r.verb, r.run)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range append([]string{c.Flag}, c.SkipIf...) {
			if !slices.ContainsFunc(flags, func(f flagSpec) bool { return f.Name == name }) {
				t.Errorf("compose telegram has no --%s", name)
			}
		}
	}
}
