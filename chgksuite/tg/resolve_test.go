package tg

import "testing"

// resolve.db is chgksuite's own file, so a name saved by either tool is found
// by the other: the Python tool stores the id without its -100.
func TestSavedIDsAreSharedWithChgksuite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if id := savedID("private"); id != 0 {
		t.Fatalf("nothing saved yet, got %d", id)
	}
	saveID("private", "-1001234567890")
	if id := savedID("private"); id != 1234567890 {
		t.Fatalf("got %d", id)
	}
	if got := prefixed(savedID("private")); got != "-1001234567890" {
		t.Fatalf("prefixed %q", got)
	}
	if got := SavedChannels(); len(got) != 1 || got[0] != "private" {
		t.Fatalf("SavedChannels %q", got)
	}
}
