package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

// The app updates itself from its GitHub releases. It looks when it opens, in
// the background and at most once a day. A release it finds is remembered, so
// the strip offers it on every start until it is installed.

const (
	releaseRepo  = "alexander-pecheny/dopesuite"
	releaseTag   = "chgksuite-app/"
	checkEvery   = 24 * time.Hour
	checkTimeout = time.Minute
	stateMode    = 0o600
)

// version is the release this build is, which the release workflow stamps
// (-X main.version=2026.10.19). A checkout is "dev" and never updates.
var version = "dev"

// updateState is update.json: when the app last looked, from which version,
// and what it found.
type updateState struct {
	CheckedAt  time.Time `json:"checked_at"`
	CheckedFor string    `json:"checked_for"`
	Available  string    `json:"available,omitempty"`
}

type updates struct {
	up    *updater.Updater
	path  string
	done  chan struct{}
	found string
}

func newUpdates(app *application.App, path string) *updates {
	u := &updates{up: app.Updater, path: path, done: make(chan struct{})}
	if version == "dev" {
		close(u.done)
		return u
	}
	gh, err := github.New(github.Config{Repository: releaseRepo, TagPrefix: releaseTag, Prerelease: true, ChecksumAsset: "SHA256SUMS"})
	if err == nil {
		err = u.up.Init(updater.Config{CurrentVersion: version, Providers: []updater.Provider{gh}, Window: updater.WindowNone})
	}
	if err != nil {
		log.Printf("updates off: %v", err)
		close(u.done)
		return u
	}
	go u.look()
	return u
}

// look asks GitHub unless it did so within the day, for this same version.
func (u *updates) look() {
	defer close(u.done)
	var st updateState
	if data, err := os.ReadFile(u.path); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	if st.CheckedFor == version && time.Since(st.CheckedAt) < checkEvery {
		u.found = st.Available
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	rel, err := u.up.Check(ctx)
	if err != nil {
		log.Printf("update check: %v", err)
		return
	}
	st = updateState{CheckedAt: time.Now(), CheckedFor: version}
	if rel != nil {
		st.Available, u.found = rel.Version, rel.Version
	}
	if data, err := json.Marshal(st); err == nil {
		_ = os.WriteFile(u.path, data, stateMode)
	}
}

// available waits for the look and returns the version it found, or "".
func (u *updates) available() string {
	<-u.done
	return u.found
}

// install downloads the release, checks it against SHA256SUMS, and restarts
// into it. A release remembered from an earlier day is looked up again first.
func (u *updates) install(ctx context.Context) error {
	if u.available() == "" {
		return errors.New("нет обновления")
	}
	if u.up.State() != updater.StateAvailable {
		if _, err := u.up.Check(ctx); err != nil {
			return err
		}
	}
	if err := u.up.DownloadAndInstall(ctx); err != nil {
		return err
	}
	return u.up.Restart(ctx)
}
