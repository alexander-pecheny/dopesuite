package main

import (
	"os"
	"path/filepath"
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
