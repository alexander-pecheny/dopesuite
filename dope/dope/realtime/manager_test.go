package realtime

import "testing"

func recvCount(t *testing.T, ch chan Event) string {
	t.Helper()
	select {
	case ev := <-ch:
		if ev.Name != "viewers" {
			t.Fatalf("event name = %q, want viewers", ev.Name)
		}
		return string(ev.Data)
	default:
		t.Fatal("expected a viewers event on subscriber channel")
		return ""
	}
}

func TestBroadcastViewerCount(t *testing.T) {
	m := NewManager()
	a := make(chan Event, 8)
	b := make(chan Event, 8)
	m.AddSubscriber(7, a, false, 0)
	m.AddSubscriber(7, b, false, 0)
	// A subscriber on a different fest must not be counted.
	m.AddSubscriber(9, make(chan Event, 8), false, 0)

	m.BroadcastViewerCount(7)
	for _, ch := range []chan Event{a, b} {
		if got, want := recvCount(t, ch), `{"count":2}`; got != want {
			t.Fatalf("payload = %s, want %s", got, want)
		}
	}

	m.RemoveSubscriber(7, b)
	m.BroadcastViewerCount(7)
	if got, want := recvCount(t, a), `{"count":1}`; got != want {
		t.Fatalf("after disconnect payload = %s, want %s", got, want)
	}
}

// TestViewerCountPerGameAndEditors asserts the tally is partitioned per game and
// excludes editors: each spectator sees the count for its own game, an editor is
// not counted but still receives its game's count.
func TestViewerCountPerGameAndEditors(t *testing.T) {
	m := NewManager()
	g1a := make(chan Event, 8)
	g1b := make(chan Event, 8)
	g2 := make(chan Event, 8)
	editor := make(chan Event, 8)
	m.AddSubscriber(7, g1a, false, 1)   // viewer of game 1
	m.AddSubscriber(7, g1b, false, 1)   // viewer of game 1
	m.AddSubscriber(7, g2, false, 2)    // viewer of game 2
	m.AddSubscriber(7, editor, true, 1) // editor of game 1 — not counted

	m.BroadcastViewerCount(7)

	// Game 1 has two spectators (the editor is excluded).
	for _, ch := range []chan Event{g1a, g1b} {
		if got, want := recvCount(t, ch), `{"count":2}`; got != want {
			t.Errorf("game 1 viewer payload = %s, want %s", got, want)
		}
	}
	// Game 2 has one spectator.
	if got, want := recvCount(t, g2), `{"count":1}`; got != want {
		t.Errorf("game 2 viewer payload = %s, want %s", got, want)
	}
	// The editor receives its game's spectator count (2), though it is not itself
	// counted.
	if got, want := recvCount(t, editor), `{"count":2}`; got != want {
		t.Errorf("editor payload = %s, want %s", got, want)
	}
}
