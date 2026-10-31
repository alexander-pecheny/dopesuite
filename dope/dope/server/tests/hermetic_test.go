package tests

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"dope/dope/domain/imports"
	"dope/dope/storage/festwrite"
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
//
// It also keeps the tests off the network and off the clock. The rating
// site's API is a fake: every tournament exists, with no questions and no
// requests, and nothing else does. A write may hold the lock for as long as a
// loaded box needs.
func TestMain(m *testing.M) {
	for _, key := range serverEnv {
		os.Unsetenv(key)
	}
	rating := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/requests"):
			_, _ = w.Write([]byte("[]"))
		case strings.HasSuffix(r.URL.Path, ".json"):
			_, _ = w.Write([]byte("{}"))
		default:
			http.NotFound(w, r)
		}
	}))
	imports.RatingAPI = rating.URL
	festwrite.WriteTxTimeout = loadedWriteTimeout
	code := m.Run()
	rating.Close()
	os.Exit(code)
}

// loadedWriteTimeout is the write deadline the tests run under.
const loadedWriteTimeout = time.Minute
