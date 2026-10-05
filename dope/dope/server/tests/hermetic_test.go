package tests

import (
	"os"
	"testing"
)

// serverEnv is every variable the server reads at request time.
var serverEnv = []string{
	"TELEGRAM_BOT_TOKEN", "DOPE_BOT_NAME", "DOPE_ADMIN_USER",
	"DOPE_TRUSTED_ORIGIN_HOSTS", "DOPE_EDIT_METRICS", "DOPE_BUFF_DB",
	"DOPE_STATIC", "DOPE_STATIC_CTL", "DOPE_STATIC_COOLDOWN", "DOPE_STATIC_RATE_HIGH",
	"DOPE_STATIC_RATE_LOW", "DOPE_STATIC_RETENTION", "DOPE_STATIC_SSE_MAX",
}

// TestMain clears the server's settings from the environment before any test
// runs. Most tests here run in parallel, so none of them may change the
// environment (t.Setenv refuses to in a parallel test); clearing it once means
// they all see the defaults, whatever the shell that ran `go test` had set. A
// test that needs a bot token asks its own server for one (Server.SetEnv). The
// few that still need t.Setenv run serially, before the parallel ones start.
// The tests' own switches (DOPE_REHEARSE_DB and the like) are left alone.
func TestMain(m *testing.M) {
	for _, key := range serverEnv {
		os.Unsetenv(key)
	}
	os.Exit(m.Run())
}
