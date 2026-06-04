package main

import "testing"

func TestBroadcastViewerCount(t *testing.T) {
	srv := &server{}
	a := make(chan event, 8)
	b := make(chan event, 8)
	srv.addSubscriber(7, a, false)
	srv.addSubscriber(7, b, false)
	// A subscriber on a different fest must not be counted.
	srv.addSubscriber(9, make(chan event, 8), false)

	srv.broadcastViewerCount(7)

	for _, ch := range []chan event{a, b} {
		select {
		case ev := <-ch:
			if ev.name != "viewers" {
				t.Fatalf("event name = %q, want viewers", ev.name)
			}
			if got, want := string(ev.data), `{"count":2}`; got != want {
				t.Fatalf("payload = %s, want %s", got, want)
			}
		default:
			t.Fatal("expected a viewers event on subscriber channel")
		}
	}

	srv.removeSubscriber(7, b)
	srv.broadcastViewerCount(7)
	select {
	case ev := <-a:
		if got, want := string(ev.data), `{"count":1}`; got != want {
			t.Fatalf("after disconnect payload = %s, want %s", got, want)
		}
	default:
		t.Fatal("expected an updated viewers event after disconnect")
	}
}
